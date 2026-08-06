# RFC 0003: Stage Model

**Status:** accepted for preview. Stages are narrow Go interfaces. Replaceable
defaults are Spice fallback beans; decorators are ordered typed collections;
tools pass through exactly one dispatcher. Stage selection happens at generated
construction time. No stage registry or string lookup exists at runtime.

