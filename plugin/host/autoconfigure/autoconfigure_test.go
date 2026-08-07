package autoconfigure

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"

	pluginhost "github.com/spice-framework/spice-agent/plugin/host"
	"github.com/spice-framework/spice-agent/plugin/host/localendpoint"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/process"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

func TestSpiceAutoConfigurationIsDeterministic(t *testing.T) {
	t.Parallel()
	descriptor := SpiceAutoConfiguration()
	wantNames := []string{
		"runtimePluginCompiledDispatcher",
		"runtimePluginEndpointFactory",
		"runtimePluginHost",
		"runtimePluginToolPlanSource",
	}
	wantFactories := []any{
		DefaultCompiledDispatcher,
		DefaultCurrentUserEndpointFactory,
		DefaultHost,
		DefaultToolPlanSource,
	}
	if descriptor.Review != "docs/dependencies.md" || len(descriptor.Beans) != len(wantNames) {
		t.Fatalf("SpiceAutoConfiguration() = %#v", descriptor)
	}
	for index, bean := range descriptor.Beans {
		if bean.Name != wantNames[index] || !bean.Fallback || bean.Primary ||
			len(bean.Aliases) != 0 || len(bean.Qualifiers) != 0 || bean.Order != 0 {
			t.Fatalf("SpiceAutoConfiguration().Beans[%d] = %#v", index, bean)
		}
		if reflect.ValueOf(bean.Factory).Pointer() != reflect.ValueOf(wantFactories[index]).Pointer() {
			t.Fatalf("SpiceAutoConfiguration().Beans[%d].Factory = %T", index, bean.Factory)
		}
	}
}

func TestDefaultCompiledDispatcherAcceptsEmptyStaticGraph(t *testing.T) {
	t.Parallel()
	dispatcher, err := DefaultCompiledDispatcher(map[string]tool.Tool{})
	if err != nil {
		t.Fatalf("DefaultCompiledDispatcher: %v", err)
	}
	if dispatcher == nil || len(dispatcher.Definitions()) != 0 {
		t.Fatalf("DefaultCompiledDispatcher() definitions = %#v", dispatcher.Definitions())
	}
}

func TestDefaultCurrentUserEndpointFactory(t *testing.T) {
	t.Parallel()
	factory := DefaultCurrentUserEndpointFactory()
	if factory == nil {
		t.Fatal("DefaultCurrentUserEndpointFactory() returned nil")
	}
	if _, ok := factory.(*localendpoint.Factory); !ok {
		t.Fatalf("DefaultCurrentUserEndpointFactory() = %T", factory)
	}
}

func TestDefaultHostRejectsMissingMandatoryDependencies(t *testing.T) {
	t.Parallel()
	dispatcher, err := DefaultCompiledDispatcher(nil)
	if err != nil {
		t.Fatalf("DefaultCompiledDispatcher: %v", err)
	}
	identity := validHostIdentity()
	launcher := inertLauncher()
	endpoints := DefaultCurrentUserEndpointFactory()
	tests := map[string]struct {
		identity   *pluginv1.BuildIdentity
		dispatcher stage.ToolDispatcher
		launcher   process.Launcher
		endpoints  pluginhost.LocalEndpointFactory
	}{
		"identity":   {nil, dispatcher, launcher, endpoints},
		"dispatcher": {identity, nil, launcher, endpoints},
		"launcher":   {identity, dispatcher, nil, endpoints},
		"endpoints":  {identity, dispatcher, launcher, nil},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			host, cleanup, hostErr := DefaultHost(
				test.identity,
				test.dispatcher,
				nil,
				test.launcher,
				test.endpoints,
			)
			if hostErr == nil || host != nil || cleanup != nil {
				t.Fatalf("DefaultHost() = (%p, %v, %v), want nil, nil, error", host, cleanup, hostErr)
			}
		})
	}
}

func TestDefaultHostProvidesInitialPlanAdapterAndCleanup(t *testing.T) {
	t.Parallel()
	dispatcher, err := DefaultCompiledDispatcher(nil)
	if err != nil {
		t.Fatalf("DefaultCompiledDispatcher: %v", err)
	}
	host, cleanup, err := DefaultHost(
		validHostIdentity(),
		dispatcher,
		nil,
		inertLauncher(),
		DefaultCurrentUserEndpointFactory(),
	)
	if err != nil || host == nil || cleanup == nil {
		t.Fatalf("DefaultHost() = (%p, %v, %v)", host, cleanup, err)
	}
	source, err := DefaultToolPlanSource(host)
	if err != nil {
		t.Fatalf("DefaultToolPlanSource: %v", err)
	}
	actual, ok := source.(*pluginhost.Host)
	if !ok || actual != host {
		t.Fatalf("DefaultToolPlanSource() = %T %p, want exact host %p", source, actual, host)
	}
	lease, err := source.LeaseCurrent(context.Background())
	if err != nil {
		t.Fatalf("LeaseCurrent: %v", err)
	}
	if len(lease.Definitions()) != 0 {
		t.Fatalf("initial plan definitions = %#v", lease.Definitions())
	}
	if err = lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err = cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err = cleanup(context.Background()); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
}

func TestDefaultToolPlanSourceRejectsNilHost(t *testing.T) {
	t.Parallel()
	if source, err := DefaultToolPlanSource(nil); err == nil || source != nil {
		t.Fatalf("DefaultToolPlanSource(nil) = (%v, %v), want nil, error", source, err)
	}
}

func inertLauncher() process.Launcher {
	return process.LauncherFunc(func(context.Context, process.Spec) (process.Process, error) {
		return nil, errors.New("inert launcher must not be called while leasing the compiled generation")
	})
}

func validHostIdentity() *pluginv1.BuildIdentity {
	return &pluginv1.BuildIdentity{
		Component: "autoconfigure-test",
		Version:   "v0",
		Commit:    "test",
		Runtime:   runtime.Version(),
	}
}
