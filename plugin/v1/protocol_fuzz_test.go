package pluginv1_test

import (
	"testing"

	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"google.golang.org/protobuf/proto"
)

func FuzzPluginEnvelope(fuzz *testing.F) {
	fuzz.Add([]byte{})
	fuzz.Add([]byte{0x08, 0x01})
	fuzz.Add([]byte{0xff, 0xff, 0xff})
	fuzz.Fuzz(func(t *testing.T, data []byte) {
		var request pluginv1.InitializeRequest
		if proto.Unmarshal(data, &request) == nil {
			_ = pluginv1.ValidateInitializeRequest(&request)
			_, _ = proto.Marshal(&request)
		}
		var response pluginv1.ExecuteResponse
		if proto.Unmarshal(data, &response) == nil {
			request := validExecuteRequest()
			validator, err := pluginv1.NewStreamValidator(request, sessionID(), validLimits())
			if err == nil {
				_, _ = validator.Accept(&response)
				_ = validator.Finish()
			}
			_, _ = proto.Marshal(&response)
		}
	})
}
