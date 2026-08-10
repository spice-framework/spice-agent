package planning

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/message"
)

const (
	// ContractVersion changes whenever canonical planning semantics change.
	ContractVersion = "spice.agent.planning/advisory-v1"
	// MaximumSteps bounds one advisory plan.
	MaximumSteps = 64
	// MaximumDependencies bounds one step's backward-only dependencies.
	MaximumDependencies = 16
	// MaximumStepIDBytes bounds one stable step identity.
	MaximumStepIDBytes = 64
	// MaximumTextBytes bounds goals and step summaries independently.
	MaximumTextBytes = 4 << 10
	// MaximumPlanBytes bounds the complete dedicated plan text part.
	MaximumPlanBytes = 64 << 10

	maximumPlannerIdentityBytes  = 256
	maximumSemanticIdentityBytes = 256
	digestPrefix                 = "sha256:"
)

var planMarker = "\n\n[Spice advisory plan " + ContractVersion + "]\n"

// ValidationError carries one fixed, content-free planning validation code.
type ValidationError struct{ Code string }

func (failure *ValidationError) Error() string {
	if failure == nil || failure.Code == "" {
		return "planning validation failed"
	}
	return "planning validation failed: " + failure.Code
}

func invalid(code string) error { return &ValidationError{Code: code} }

// Step is one immutable ordered advisory action.
type Step struct {
	id        string
	summary   string
	dependsOn []string
}

// NewStep constructs one bounded step. Dependency existence and ordering are
// validated when the enclosing Draft is constructed.
func NewStep(id, summary string, dependsOn ...string) (Step, error) {
	if err := validateToken(id, MaximumStepIDBytes); err != nil {
		return Step{}, invalid("step_id")
	}
	if err := validateText(summary); err != nil {
		return Step{}, invalid("step_summary")
	}
	if len(dependsOn) > MaximumDependencies {
		return Step{}, invalid("step_dependencies")
	}
	seen := make(map[string]struct{}, len(dependsOn))
	for _, dependency := range dependsOn {
		if err := validateToken(dependency, MaximumStepIDBytes); err != nil {
			return Step{}, invalid("step_dependency")
		}
		if _, duplicate := seen[dependency]; duplicate {
			return Step{}, invalid("duplicate_step_dependency")
		}
		seen[dependency] = struct{}{}
	}
	return Step{id: id, summary: summary, dependsOn: append([]string(nil), dependsOn...)}, nil
}

func (step Step) validate() error {
	_, err := NewStep(step.id, step.summary, step.dependsOn...)
	return err
}

// ID returns the stable step identity.
func (step Step) ID() string { return step.id }

// Summary returns the bounded advisory text.
func (step Step) Summary() string { return step.summary }

// DependsOn returns a defensive ordered dependency copy.
func (step Step) DependsOn() []string { return append([]string(nil), step.dependsOn...) }

func (step Step) clone() Step {
	step.dependsOn = append([]string(nil), step.dependsOn...)
	return step
}

// Draft is immutable planner output that has not yet been bound to an input.
type Draft struct {
	goal  string
	steps []Step
}

// NewDraft validates deterministic order and backward-only dependencies.
func NewDraft(goal string, steps ...Step) (Draft, error) {
	if err := validateText(goal); err != nil {
		return Draft{}, invalid("goal")
	}
	if len(steps) == 0 || len(steps) > MaximumSteps {
		return Draft{}, invalid("step_count")
	}
	seen := make(map[string]struct{}, len(steps))
	result := Draft{goal: goal, steps: make([]Step, len(steps))}
	for index, step := range steps {
		if err := step.validate(); err != nil {
			return Draft{}, err
		}
		if _, duplicate := seen[step.id]; duplicate {
			return Draft{}, invalid("duplicate_step_id")
		}
		for _, dependency := range step.dependsOn {
			if dependency == step.id {
				return Draft{}, invalid("self_dependency")
			}
			if _, known := seen[dependency]; !known {
				return Draft{}, invalid("forward_or_unknown_dependency")
			}
		}
		seen[step.id] = struct{}{}
		result.steps[index] = step.clone()
	}
	return result, nil
}

func (draft Draft) validate() error {
	_, err := NewDraft(draft.goal, draft.steps...)
	return err
}

// Goal returns the immutable goal text.
func (draft Draft) Goal() string { return draft.goal }

// Steps returns defensive immutable copies.
func (draft Draft) Steps() []Step {
	result := make([]Step, len(draft.steps))
	for index, step := range draft.steps {
		result[index] = step.clone()
	}
	return result
}

// Request is immutable planner input and carries its canonical input digest.
type Request struct {
	definition  agent.Definition
	initial     message.Message
	inputDigest string
}

// NewRequest validates one unplanned initial user message.
func NewRequest(definition agent.Definition, initial message.Message) (Request, error) {
	if _, err := agent.NewDefinition(definition.Name(), definition.Model(), definition.MaxTurns()); err != nil {
		return Request{}, invalid("definition")
	}
	if err := initial.Validate(); err != nil || initial.Role() != message.RoleUser {
		return Request{}, invalid("initial_message")
	}
	if _, found, err := extractAttached(definition, initial); err != nil || found {
		return Request{}, invalid("existing_plan")
	}
	return Request{
		definition: definition, initial: initial.Clone(),
		inputDigest: inputDigest(definition, initial),
	}, nil
}

func (request Request) validate() error {
	rebuilt, err := NewRequest(request.definition, request.initial)
	if err != nil {
		return err
	}
	if request.inputDigest != rebuilt.inputDigest {
		return invalid("input_digest")
	}
	return nil
}

// Definition returns the immutable worker definition.
func (request Request) Definition() agent.Definition { return request.definition }

// Initial returns a defensive copy of the original user message.
func (request Request) Initial() message.Message { return request.initial.Clone() }

// InputDigest returns the canonical SHA-256 identity of definition and input.
func (request Request) InputDigest() string { return request.inputDigest }

// Plan is an immutable canonical advisory plan attached to durable history.
type Plan struct {
	id          string
	producer    string
	inputDigest string
	goal        string
	steps       []Step
	encoded     []byte
}

// Finalize binds a validated Draft to one exact planner identity and input.
func Finalize(request Request, producer string, draft Draft) (Plan, error) {
	if err := request.validate(); err != nil {
		return Plan{}, err
	}
	if err := validatePlannerIdentity(producer); err != nil {
		return Plan{}, invalid("planner_identity")
	}
	if err := draft.validate(); err != nil {
		return Plan{}, err
	}
	return finalize(producer, request.inputDigest, draft)
}

func finalize(producer, sourceDigest string, draft Draft) (Plan, error) {
	payload := payloadWire{
		Version: ContractVersion, Producer: producer, InputDigest: sourceDigest,
		Goal: draft.goal, Steps: stepWires(draft.steps),
	}
	digestBytes, err := json.Marshal(payload)
	if err != nil {
		return Plan{}, invalid("encoding")
	}
	digest := sha256.Sum256(digestBytes)
	id := digestPrefix + hex.EncodeToString(digest[:])
	encoded, err := json.Marshal(planWire{
		Version: payload.Version, PlanID: id, Producer: payload.Producer,
		InputDigest: payload.InputDigest, Goal: payload.Goal, Steps: payload.Steps,
	})
	if err != nil || len(planMarker)+len(encoded) > MaximumPlanBytes {
		return Plan{}, invalid("encoded_size")
	}
	return Plan{
		id: id, producer: producer, inputDigest: sourceDigest, goal: draft.goal,
		steps: draft.Steps(), encoded: append([]byte(nil), encoded...),
	}, nil
}

// ParsePlan accepts exactly one canonical plan JSON value.
func ParsePlan(encoded []byte) (Plan, error) {
	if len(encoded) == 0 || len(planMarker)+len(encoded) > MaximumPlanBytes {
		return Plan{}, invalid("encoded_size")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire planWire
	if err := decoder.Decode(&wire); err != nil {
		return Plan{}, invalid("encoding")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Plan{}, invalid("trailing_data")
	}
	if wire.Version != ContractVersion || !validDigest(wire.PlanID) || !validDigest(wire.InputDigest) {
		return Plan{}, invalid("identity")
	}
	steps := make([]Step, len(wire.Steps))
	for index, value := range wire.Steps {
		step, err := NewStep(value.ID, value.Summary, value.DependsOn...)
		if err != nil {
			return Plan{}, err
		}
		steps[index] = step
	}
	draft, err := NewDraft(wire.Goal, steps...)
	if err != nil {
		return Plan{}, err
	}
	if err = validatePlannerIdentity(wire.Producer); err != nil {
		return Plan{}, invalid("planner_identity")
	}
	result, err := finalize(wire.Producer, wire.InputDigest, draft)
	if err != nil {
		return Plan{}, err
	}
	if result.id != wire.PlanID || !bytes.Equal(encoded, result.encoded) {
		return Plan{}, invalid("canonical_identity")
	}
	return result, nil
}

func (plan Plan) validate() error {
	parsed, err := ParsePlan(plan.encoded)
	if err != nil {
		return err
	}
	if plan.id != parsed.id || plan.producer != parsed.producer ||
		plan.inputDigest != parsed.inputDigest || plan.goal != parsed.goal ||
		!equalSteps(plan.steps, parsed.steps) {
		return invalid("plan_state")
	}
	return nil
}

// ID returns the canonical SHA-256 plan identity.
func (plan Plan) ID() string { return plan.id }

// Producer returns the exact planner semantic identity.
func (plan Plan) Producer() string { return plan.producer }

// InputDigest returns the exact source input identity.
func (plan Plan) InputDigest() string { return plan.inputDigest }

// Goal returns the immutable goal.
func (plan Plan) Goal() string { return plan.goal }

// Steps returns defensive immutable copies.
func (plan Plan) Steps() []Step {
	result := make([]Step, len(plan.steps))
	for index, step := range plan.steps {
		result[index] = step.clone()
	}
	return result
}

// CanonicalJSON returns a defensive copy of the exact durable representation.
func (plan Plan) CanonicalJSON() []byte { return append([]byte(nil), plan.encoded...) }

// Attach appends one dedicated canonical text part without mutating the source.
func Attach(request Request, plan Plan) (message.Message, error) {
	if err := request.validate(); err != nil {
		return message.Message{}, err
	}
	if err := plan.validate(); err != nil {
		return message.Message{}, err
	}
	if plan.inputDigest != request.inputDigest {
		return message.Message{}, invalid("input_mismatch")
	}
	part, err := message.Text(planMarker + string(plan.encoded))
	if err != nil {
		return message.Message{}, invalid("message_capacity")
	}
	parts := request.initial.Parts()
	parts = append(parts, part)
	attached, err := message.New(request.initial.ID(), request.initial.Role(), parts...)
	if err != nil {
		return message.Message{}, invalid("message_capacity")
	}
	return attached, nil
}

// ExtractMessage returns and validates the one dedicated plan attached to an
// initial user message. Incidental marker text inside ordinary parts is ignored.
func ExtractMessage(definition agent.Definition, initial message.Message) (Plan, bool, error) {
	return extractAttached(definition, initial)
}

// Extract returns the exact plan from persisted snapshot history.
func Extract(snapshot agent.Snapshot) (Plan, bool, error) {
	if err := snapshot.Validate(); err != nil {
		return Plan{}, false, invalid("snapshot")
	}
	history := snapshot.History()
	if len(history) == 0 {
		return Plan{}, false, invalid("snapshot_history")
	}
	return extractAttached(snapshot.Definition(), history[0])
}

func extractAttached(definition agent.Definition, initial message.Message) (Plan, bool, error) {
	if err := initial.Validate(); err != nil || initial.Role() != message.RoleUser {
		return Plan{}, false, invalid("initial_message")
	}
	parts := initial.Parts()
	found := -1
	var parsed Plan
	for index, part := range parts {
		text, textPart := part.TextValue()
		if !textPart || !strings.HasPrefix(text, planMarker) {
			continue
		}
		if found >= 0 {
			return Plan{}, false, invalid("duplicate_plan")
		}
		value, err := ParsePlan([]byte(strings.TrimPrefix(text, planMarker)))
		if err != nil {
			return Plan{}, false, err
		}
		found, parsed = index, value
	}
	if found < 0 {
		return Plan{}, false, nil
	}
	originalParts := slices.Delete(parts, found, found+1)
	if len(originalParts) == 0 {
		return Plan{}, false, invalid("missing_original_input")
	}
	original, err := message.New(initial.ID(), initial.Role(), originalParts...)
	if err != nil {
		return Plan{}, false, invalid("initial_message")
	}
	if parsed.inputDigest != inputDigest(definition, original) {
		return Plan{}, false, invalid("input_mismatch")
	}
	return parsed, true, nil
}

// SemanticIdentity binds snapshot portability to the planner and all local
// canonical planning semantics without exposing user or plan content.
func SemanticIdentity(application, plannerIdentity string) (string, error) {
	if err := validateToken(application, maximumSemanticIdentityBytes); err != nil {
		return "", invalid("application_identity")
	}
	if err := validatePlannerIdentity(plannerIdentity); err != nil {
		return "", invalid("planner_identity")
	}
	material := fmt.Sprintf(
		"%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%s",
		ContractVersion, planMarker, MaximumSteps, MaximumDependencies,
		MaximumTextBytes, MaximumPlanBytes, plannerIdentity,
	)
	digest := sha256.Sum256([]byte(material))
	suffix := fmt.Sprintf("|planning:%x", digest[:16])
	if len(application)+len(suffix) > maximumSemanticIdentityBytes {
		return "", invalid("semantic_identity_size")
	}
	return application + suffix, nil
}

type stepWire struct {
	ID        string   `json:"id"`
	Summary   string   `json:"summary"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type payloadWire struct {
	Version     string     `json:"version"`
	Producer    string     `json:"producer"`
	InputDigest string     `json:"input_sha256"`
	Goal        string     `json:"goal"`
	Steps       []stepWire `json:"steps"`
}

type planWire struct {
	Version     string     `json:"version"`
	PlanID      string     `json:"plan_sha256"`
	Producer    string     `json:"producer"`
	InputDigest string     `json:"input_sha256"`
	Goal        string     `json:"goal"`
	Steps       []stepWire `json:"steps"`
}

func stepWires(steps []Step) []stepWire {
	result := make([]stepWire, len(steps))
	for index, step := range steps {
		result[index] = stepWire{ID: step.id, Summary: step.summary, DependsOn: append([]string(nil), step.dependsOn...)}
	}
	return result
}

func equalSteps(left, right []Step) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].id != right[index].id || left[index].summary != right[index].summary ||
			!slices.Equal(left[index].dependsOn, right[index].dependsOn) {
			return false
		}
	}
	return true
}

func validatePlannerIdentity(value string) error {
	return validateToken(value, maximumPlannerIdentityBytes)
}

func validateText(value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > MaximumTextBytes || !utf8.ValidString(value) {
		return errors.New("invalid planning text")
	}
	return nil
}

func validateToken(value string, maximum int) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maximum || !utf8.ValidString(value) {
		return errors.New("invalid planning token")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return errors.New("invalid planning token")
		}
	}
	return nil
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, digestPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, digestPrefix)
	if len(encoded) != sha256.Size*2 || strings.ToLower(encoded) != encoded {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

type hashWriter interface{ Write([]byte) (int, error) }

func inputDigest(definition agent.Definition, initial message.Message) string {
	hash := sha256.New()
	writeField(hash, []byte("spice-agent-planning-input/v1"))
	writeField(hash, []byte(definition.Name()))
	writeField(hash, []byte(definition.Model()))
	var turns [4]byte
	binary.BigEndian.PutUint32(turns[:], definition.MaxTurns())
	writeField(hash, turns[:])
	writeField(hash, []byte(initial.ID()))
	writeField(hash, []byte(initial.Role()))
	for _, part := range initial.Parts() {
		writeField(hash, []byte(part.Kind()))
		text, _ := part.TextValue()
		writeField(hash, []byte(text))
		writeField(hash, []byte(part.Name()))
		writeField(hash, []byte(part.CallID()))
		writeField(hash, []byte(part.Namespace()))
		writeField(hash, part.Data())
	}
	return digestPrefix + hex.EncodeToString(hash.Sum(nil))
}

func writeField(destination hashWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write(value)
}
