package autoconfigure

import (
	"reflect"
	"testing"

	agentlogging "github.com/spice-framework/spice-agent/logging"
	spicelogging "github.com/spice-framework/spice/logging"
)

func TestSpiceAutoConfigurationIsDeterministic(t *testing.T) {
	t.Parallel()
	descriptor := SpiceAutoConfiguration()
	wantNames := []string{
		"agentLoggingConfig", "agentLoggingMailbox", "agentLoggingProcessor", "agentLoggingHealth",
	}
	wantFactories := []any{DefaultConfig, DefaultMailbox, DefaultProcessor, DefaultHealthSource}
	if descriptor.Review != "docs/dependencies.md" || len(descriptor.Beans) != len(wantNames) {
		t.Fatalf("SpiceAutoConfiguration() = %#v", descriptor)
	}
	for index, bean := range descriptor.Beans {
		if bean.Name != wantNames[index] || !bean.Fallback || bean.Primary ||
			len(bean.Aliases) != 0 || len(bean.Qualifiers) != 0 || bean.Order != 0 {
			t.Fatalf("bean %d = %#v", index, bean)
		}
		if reflect.ValueOf(bean.Factory).Pointer() != reflect.ValueOf(wantFactories[index]).Pointer() {
			t.Fatalf("bean %d factory = %T", index, bean.Factory)
		}
	}
}

func TestDefaultsConstructOneProcessorAndHealthSource(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	mailbox, err := DefaultMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := spicelogging.New(spicelogging.Options{
		Application: "autoconfigure-test",
		Configuration: spicelogging.Configuration{
			Format: spicelogging.FormatJSON, Level: spicelogging.LevelInfo,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	processor, cleanup, err := DefaultProcessor(config, mailbox, logger)
	if err != nil {
		t.Fatal(err)
	}
	if processor == nil || cleanup == nil {
		t.Fatal("processor construction returned nil")
	}
	health := DefaultHealthSource(config, processor)
	if _, ok := health.(*agentlogging.HealthSource); !ok {
		t.Fatalf("health source = %T", health)
	}
	if err = cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultProcessorRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	mailbox, err := DefaultMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	if processor, cleanup, err := DefaultProcessor(config, mailbox, nil); err == nil || processor != nil || cleanup != nil {
		t.Fatal("nil logger succeeded")
	}
	if DefaultHealthSource(config, nil) == nil {
		t.Fatal("nil processor health source is nil")
	}
}
