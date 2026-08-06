package compositionwrongoutput

import (
	"context"

	"github.com/spice-framework/spice-agent/tool"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

type concreteTool struct{}

func (*concreteTool) Definition() tool.Definition { return tool.Definition{} }

func (*concreteTool) Execute(context.Context, tool.Call, tool.Reporter) (tool.Result, error) {
	return tool.Result{}, nil
}

// @Bean(name="concrete")
func Concrete() *concreteTool { return &concreteTool{} }

type consumer struct{}

// @Bean
func Consume(tool.Tool) *consumer { return &consumer{} }
