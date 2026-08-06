package agent

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"slices"
	"strings"

	"github.com/spice-framework/spice/annotation/sdk"
)

const (
	descriptorPackage = "github.com/spice-framework/spice-agent/annotation/agent"
	maximumOrder      = int64(1_000_000)
)

type factoryContract struct {
	requireName bool
}

func providerMetadata(
	ctx context.Context,
	invocation sdk.Invocation,
	symbol string,
	contract factoryContract,
) (sdk.Result, error) {
	if ctx == nil {
		return sdk.Result{}, errors.New("agent annotation handler context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return sdk.Result{}, err
	}
	if err := invocation.RequireDescriptor(descriptorPackage, symbol); err != nil {
		return sdk.Result{}, err
	}
	if err := validateFactory(invocation); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"name",
		"aliases",
		"qualifiers",
		"fallback",
		"primary",
		"order",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	name, aliases, err := sdk.BeanIdentity(arguments)
	if err != nil {
		return sdk.Result{}, err
	}
	if contract.requireName && name == "" {
		return sdk.Result{}, errors.New("annotation argument \"name\" is required")
	}
	if validationErr := validateIdentities(name, aliases); validationErr != nil {
		return sdk.Result{}, validationErr
	}
	qualifiers, err := arguments.Strings("qualifiers")
	if err != nil {
		return sdk.Result{}, err
	}
	if validationErr := validateIdentitySet("qualifier", qualifiers); validationErr != nil {
		return sdk.Result{}, validationErr
	}
	primary, err := arguments.Boolean("primary")
	if err != nil {
		return sdk.Result{}, err
	}
	fallback, err := arguments.Boolean("fallback")
	if err != nil {
		return sdk.Result{}, err
	}
	if primary && fallback {
		return sdk.Result{}, errors.New("agent bean cannot be both primary and fallback")
	}
	orderValue, ordered, err := boundedOrder(arguments)
	if err != nil {
		return sdk.Result{}, err
	}
	var order *int64
	if ordered {
		order = &orderValue
	}
	return sdk.Contributions(
		sdk.Contribution{
			Kind: sdk.ContributionProvider,
			Provider: &sdk.ProviderContribution{
				Name:    name,
				Aliases: aliases,
			},
		},
		sdk.Contribution{
			Kind: sdk.ContributionBeanMetadata,
			BeanMetadata: &sdk.BeanMetadataContribution{
				Qualifiers: qualifiers,
				Primary:    primary,
				Fallback:   fallback,
				Order:      order,
			},
		},
	)
}

func validateFactory(invocation sdk.Invocation) error {
	declaration := invocation.Declaration
	if declaration.Target != sdk.TargetFunction {
		return errors.New("agent annotation target must be a package-level function")
	}
	if !token.IsExported(declaration.Name) {
		return errors.New("agent annotation target factory must be exported")
	}
	if strings.TrimSpace(declaration.PackagePath) == "" ||
		strings.TrimSpace(declaration.SymbolID) == "" {
		return errors.New("agent annotation target requires package and symbol identity")
	}
	if declaration.TypeID != strings.TrimSpace(declaration.TypeID) {
		return errors.New("agent annotation target type identity must be trimmed")
	}
	if receiver := invocation.Facts["receiver"]; receiver != "" {
		return errors.New("agent annotation target must not be a method")
	}
	if kind := invocation.Facts["symbol_kind"]; kind != "" && kind != "function" {
		return errors.New("agent annotation target must resolve to a function")
	}
	_, err := firstFunctionResult(declaration.TypeID)
	return err
}

func firstFunctionResult(typeID string) (string, error) {
	if !strings.HasPrefix(typeID, "func(") {
		return "", errors.New("agent annotation target must have a non-generic function signature")
	}
	depth := 0
	for index := len("func"); index < len(typeID); index++ {
		switch typeID[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return firstResultField(strings.TrimSpace(typeID[index+1:]))
			}
			if depth < 0 {
				return "", errors.New("agent annotation target has an invalid function type identity")
			}
		}
	}
	return "", errors.New("agent annotation target has an incomplete function type identity")
}

func firstResultField(results string) (string, error) {
	if results == "" {
		return "", errors.New("agent annotation factory must return a provider value")
	}
	if results[0] != '(' {
		return results, nil
	}
	depth := 0
	for index := 0; index < len(results); index++ {
		switch results[index] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				first := strings.TrimSpace(results[1:index])
				if first == "" {
					return "", errors.New("agent annotation factory must return a provider value")
				}
				return first, nil
			}
		case ',':
			if depth == 1 {
				first := strings.TrimSpace(results[1:index])
				if first == "" {
					return "", errors.New("agent annotation factory has an empty first result")
				}
				return first, nil
			}
		}
	}
	return "", errors.New("agent annotation target has an incomplete result type identity")
}

func validateIdentities(name string, aliases []string) error {
	if name != "" {
		if err := validateCanonicalIdentity("name", name); err != nil {
			return err
		}
	}
	if err := validateIdentitySet("alias", aliases); err != nil {
		return err
	}
	if name != "" && slices.Contains(aliases, name) {
		return fmt.Errorf("agent bean alias %q duplicates its name", name)
	}
	return nil
}

func validateIdentitySet(label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateCanonicalIdentity(label, value); err != nil {
			return err
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("agent bean %s %q is duplicated", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateCanonicalIdentity(label, value string) error {
	if len(value) == 0 || len(value) > 128 || value != strings.TrimSpace(value) {
		return fmt.Errorf("agent bean %s must be a canonical identity of 1 to 128 bytes", label)
	}
	separator := false
	for index, character := range []byte(value) {
		letter := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		currentSeparator := character == '.' || character == '-' || character == '_'
		if index == 0 && !letter || index > 0 && !letter && !digit && !currentSeparator ||
			currentSeparator && separator {
			return fmt.Errorf("agent bean %s %q is not canonical", label, value)
		}
		separator = currentSeparator
	}
	if separator {
		return fmt.Errorf("agent bean %s %q is not canonical", label, value)
	}
	return nil
}

func boundedOrder(arguments sdk.BoundArguments) (int64, bool, error) {
	if _, present := arguments["order"]; !present {
		return 0, false, nil
	}
	value, err := arguments.Integer("order")
	if err != nil {
		return 0, false, err
	}
	if value < -maximumOrder || value > maximumOrder {
		return 0, false, fmt.Errorf("annotation argument \"order\" must be between %d and %d", -maximumOrder, maximumOrder)
	}
	return value, true, nil
}
