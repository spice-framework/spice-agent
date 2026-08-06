package compositionambiguity

import (
	"context"

	"github.com/spice-framework/spice-agent/tool"
)

// @import { Tool } from "github.com/spice-framework/spice-agent/annotation/agent"
// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

type implementation struct{}

func (*implementation) Definition() tool.Definition { return tool.Definition{} }

func (*implementation) Execute(context.Context, tool.Call, tool.Reporter) (tool.Result, error) {
	return tool.Result{}, nil
}

// @Tool(name="first")
func First() tool.Tool { return &implementation{} }

// @Tool(name="second")
func Second() tool.Tool { return &implementation{} }

type consumer struct{}

// @Bean
func Consume(tool.Tool) *consumer { return &consumer{} }
