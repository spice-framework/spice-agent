package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestKernelRuntimeBenchmarkBudgetsAreExactAndStable(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	budgets, err := loadKernelRuntimeBenchmarkBudgets(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(budgets[2].Rationale, "typed start and terminal occurrences") {
		t.Fatalf("tool-round rationale = %q", budgets[2].Rationale)
	}

	definition, _, err := readCanonicalJSON[benchmarkBudgetFile](root, benchmarkBudgetPath)
	if err != nil {
		t.Fatal(err)
	}
	definition.Benchmarks[0].MaximumNS++
	if err := validateKernelRuntimeBenchmarkBudgets(definition); err == nil {
		t.Fatal("widened benchmark ceiling succeeded")
	}
	definition.Benchmarks[0].MaximumNS--
	definition.MaterialTimeIncreasePercent++
	if err := validateKernelRuntimeBenchmarkBudgets(definition); err == nil {
		t.Fatal("changed material-regression threshold succeeded")
	}
}

func TestKernelRuntimeBenchmarkMedianAndBudgetEnforcement(t *testing.T) {
	t.Parallel()
	output := benchmarkTestOutput("BenchmarkKernelExample", []int{50, 10, 30, 20, 40})
	measurements, err := parseBenchmarkMeasurements(output, "BenchmarkKernelExample")
	if err != nil {
		t.Fatal(err)
	}
	median := medianBenchmarkMeasurement(measurements)
	if median.NSPerOp != 30 || median.BytesPerOp != 20 || median.AllocsPerOp != 2 {
		t.Fatalf("median = %#v", median)
	}
	budget := benchmarkBudget{
		Name: "BenchmarkKernelExample", ReferenceNS: 10,
		MaximumNS: 20, MaximumBytes: 10, MaximumAllocs: 1, Rationale: "test path",
	}
	err = enforceKernelRuntimeBenchmarkBudget(budget, median)
	if err == nil || !strings.Contains(err.Error(), "time 30 > 20 ns/op") ||
		!strings.Contains(err.Error(), "memory 20 > 10 B/op") ||
		!strings.Contains(err.Error(), "allocations 2 > 1 allocs/op") {
		t.Fatalf("budget enforcement error = %v", err)
	}
}

func TestKernelRuntimeBenchmarkParserRejectsMalformedOutput(t *testing.T) {
	t.Parallel()
	for _, output := range []string{
		"",
		strings.Repeat("BenchmarkKernelExample-1 500 10 ns/op 1 B/op 1 allocs/op\n", 4),
		strings.Repeat("BenchmarkKernelExample-1 500 invalid ns/op 1 B/op 1 allocs/op\n", 5),
		strings.Repeat("BenchmarkKernelExample-1 500 10 ns/op\n", 5),
		strings.Repeat("BenchmarkKernelExample-1 500 10 ns/op 1 MB/s 2 widgets/op\n", 5),
	} {
		if _, err := parseBenchmarkMeasurements(output, "BenchmarkKernelExample"); err == nil {
			t.Errorf("malformed benchmark output succeeded: %q", output)
		}
	}
}

func benchmarkTestOutput(name string, latencies []int) string {
	var result strings.Builder
	for _, latency := range latencies {
		fmt.Fprintf(&result, "%s-1 500 %d ns/op 20 B/op 2 allocs/op\n", name, latency)
	}
	return result.String()
}
