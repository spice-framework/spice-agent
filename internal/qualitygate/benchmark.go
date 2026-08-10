package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	benchmarkBudgetPath   = "benchmarks/budgets.json"
	benchmarkBudgetSchema = "spice.agent.benchmarks/v1"
	benchmarkSampleCount  = 5
)

var benchmarkNamePattern = regexp.MustCompile(`^BenchmarkKernel[A-Za-z0-9]+$`)

type benchmarkBudgetFile struct {
	Schema                            string            `json:"schema"`
	Module                            string            `json:"module"`
	Go                                string            `json:"go"`
	Arguments                         []string          `json:"arguments"`
	Samples                           int               `json:"samples"`
	Aggregation                       string            `json:"aggregation"`
	MaterialTimeIncreasePercent       int               `json:"material_time_increase_percent"`
	MaterialAllocationIncreasePercent int               `json:"material_allocation_increase_percent"`
	BudgetChangePolicy                string            `json:"budget_change_policy"`
	Benchmarks                        []benchmarkBudget `json:"benchmarks"`
}

type benchmarkBudget struct {
	Name          string  `json:"name"`
	ReferenceNS   float64 `json:"reference_ns_per_op"`
	MaximumNS     float64 `json:"maximum_ns_per_op"`
	MaximumBytes  float64 `json:"maximum_bytes_per_op"`
	MaximumAllocs float64 `json:"maximum_allocs_per_op"`
	Rationale     string  `json:"rationale"`
}

type benchmarkMeasurement struct {
	NSPerOp     float64
	BytesPerOp  float64
	AllocsPerOp float64
}

func expectedKernelRuntimeBudgets() []benchmarkBudget {
	return []benchmarkBudget{
		{
			Name: "BenchmarkKernelEngineConstruction", ReferenceNS: 1581,
			MaximumNS: 50000, MaximumBytes: 4096, MaximumAllocs: 64,
			Rationale: "Bounds construction and empty shutdown well below the public-runtime millisecond budget while retaining cross-host headroom.",
		},
		{
			Name: "BenchmarkKernelTextRun", ReferenceNS: 19287,
			MaximumNS: 500000, MaximumBytes: 32768, MaximumAllocs: 256,
			Rationale: "Bounds a provider-neutral text run through its authoritative terminal without including provider or transport latency.",
		},
		{
			Name: "BenchmarkKernelToolRound", ReferenceNS: 52205,
			MaximumNS: 1000000, MaximumBytes: 65536, MaximumAllocs: 768,
			Rationale: "Bounds one compiled tool round including typed start and terminal occurrences plus model continuation.",
		},
		{
			Name: "BenchmarkKernelCancellation", ReferenceNS: 23325,
			MaximumNS: 500000, MaximumBytes: 32768, MaximumAllocs: 320,
			Rationale: "Bounds cooperative cancellation from an active model stream through the single authoritative run terminal.",
		},
	}
}

func loadKernelRuntimeBenchmarkBudgets(root string) ([]benchmarkBudget, error) {
	definition, _, err := readCanonicalJSON[benchmarkBudgetFile](root, benchmarkBudgetPath)
	if err != nil {
		return nil, err
	}
	if err := validateKernelRuntimeBenchmarkBudgets(definition); err != nil {
		return nil, err
	}
	return slices.Clone(definition.Benchmarks), nil
}

func validateKernelRuntimeBenchmarkBudgets(definition benchmarkBudgetFile) error {
	wantBudgets := expectedKernelRuntimeBudgets()
	if definition.Schema != benchmarkBudgetSchema || definition.Module != modulePath ||
		definition.Go != requiredGoVersion || !slices.Equal(definition.Arguments, kernelRuntimeBenchmarkArguments()) ||
		definition.Samples != benchmarkSampleCount || definition.Aggregation != "median" ||
		definition.MaterialTimeIncreasePercent != 20 || definition.MaterialAllocationIncreasePercent != 10 ||
		definition.BudgetChangePolicy != "measured-evidence-and-reviewed-rationale" ||
		!slices.Equal(definition.Benchmarks, wantBudgets) {
		return errors.New("kernel runtime benchmark budgets differ from the reviewed stable contract")
	}
	for _, budget := range definition.Benchmarks {
		if !benchmarkNamePattern.MatchString(budget.Name) || budget.ReferenceNS <= 0 ||
			budget.MaximumNS < budget.ReferenceNS || budget.MaximumBytes <= 0 || budget.MaximumAllocs <= 0 ||
			strings.TrimSpace(budget.Rationale) == "" {
			return fmt.Errorf("kernel runtime benchmark budget %q is invalid", budget.Name)
		}
	}
	return nil
}

func parseBenchmarkMeasurements(output, name string) ([]benchmarkMeasurement, error) {
	measurements := make([]benchmarkMeasurement, 0, benchmarkSampleCount)
	scanner := bufio.NewScanner(bytes.NewBufferString(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[0] != name && !strings.HasPrefix(fields[0], name+"-") {
			continue
		}
		measurement, err := parseBenchmarkFields(fields)
		if err != nil {
			return nil, fmt.Errorf("parse benchmark %s: %w", name, err)
		}
		measurements = append(measurements, measurement)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse benchmark %s output: %w", name, err)
	}
	if len(measurements) != benchmarkSampleCount {
		return nil, fmt.Errorf(
			"parse benchmark %s: got %d measurement(s), require %d",
			name, len(measurements), benchmarkSampleCount,
		)
	}
	return measurements, nil
}

func parseBenchmarkFields(fields []string) (benchmarkMeasurement, error) {
	var measurement benchmarkMeasurement
	var foundNS, foundBytes, foundAllocs bool
	for index, field := range fields {
		switch field {
		case "ns/op":
			value, err := precedingBenchmarkMetric(fields, index)
			if err != nil {
				return benchmarkMeasurement{}, err
			}
			measurement.NSPerOp = value
			foundNS = true
		case "B/op":
			value, err := precedingBenchmarkMetric(fields, index)
			if err != nil {
				return benchmarkMeasurement{}, err
			}
			measurement.BytesPerOp = value
			foundBytes = true
		case "allocs/op":
			value, err := precedingBenchmarkMetric(fields, index)
			if err != nil {
				return benchmarkMeasurement{}, err
			}
			measurement.AllocsPerOp = value
			foundAllocs = true
		}
	}
	if !foundNS || !foundBytes || !foundAllocs || measurement.NSPerOp <= 0 ||
		measurement.BytesPerOp < 0 || measurement.AllocsPerOp < 0 {
		return benchmarkMeasurement{}, errors.New("required benchmark metrics are missing")
	}
	return measurement, nil
}

func precedingBenchmarkMetric(fields []string, index int) (float64, error) {
	if index == 0 {
		return 0, fmt.Errorf("metric %q has no value", fields[index])
	}
	value, err := strconv.ParseFloat(fields[index-1], 64)
	if err != nil {
		return 0, fmt.Errorf("parse metric %q value %q: %w", fields[index], fields[index-1], err)
	}
	return value, nil
}

func medianBenchmarkMeasurement(measurements []benchmarkMeasurement) benchmarkMeasurement {
	nsValues := make([]float64, len(measurements))
	byteValues := make([]float64, len(measurements))
	allocValues := make([]float64, len(measurements))
	for index, measurement := range measurements {
		nsValues[index] = measurement.NSPerOp
		byteValues[index] = measurement.BytesPerOp
		allocValues[index] = measurement.AllocsPerOp
	}
	slices.Sort(nsValues)
	slices.Sort(byteValues)
	slices.Sort(allocValues)
	middle := len(measurements) / 2
	return benchmarkMeasurement{
		NSPerOp: nsValues[middle], BytesPerOp: byteValues[middle], AllocsPerOp: allocValues[middle],
	}
}

func enforceKernelRuntimeBenchmarkBudget(budget benchmarkBudget, measurement benchmarkMeasurement) error {
	var violations strings.Builder
	if measurement.NSPerOp > budget.MaximumNS {
		writeBenchmarkViolation(&violations, "time", measurement.NSPerOp, budget.MaximumNS, "ns/op")
	}
	if measurement.BytesPerOp > budget.MaximumBytes {
		writeBenchmarkViolation(&violations, "memory", measurement.BytesPerOp, budget.MaximumBytes, "B/op")
	}
	if measurement.AllocsPerOp > budget.MaximumAllocs {
		writeBenchmarkViolation(&violations, "allocations", measurement.AllocsPerOp, budget.MaximumAllocs, "allocs/op")
	}
	if violations.Len() != 0 {
		return fmt.Errorf(
			"benchmark %s exceeded its stable budget:%s improve the implementation or record measured evidence and reviewed rationale before changing %s",
			budget.Name, violations.String(), benchmarkBudgetPath,
		)
	}
	return nil
}

func writeBenchmarkViolation(target *strings.Builder, label string, actual, maximum float64, unit string) {
	target.WriteByte(' ')
	target.WriteString(label)
	target.WriteByte(' ')
	target.WriteString(strconv.FormatFloat(actual, 'f', 0, 64))
	target.WriteString(" > ")
	target.WriteString(strconv.FormatFloat(maximum, 'f', 0, 64))
	target.WriteByte(' ')
	target.WriteString(unit)
	target.WriteByte(';')
}
