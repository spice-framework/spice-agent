# RFC 0008: Security Responsibility

**Status:** accepted for preview. Compiled extensions, annotation tools, and
runtime plugins are trusted code. Process separation is not sandboxing. All tool
calls traverse one typed dispatcher so a future policy decorator can intercept
them. The bare distribution warns that tools inherit user privileges. Secrets
are redacted and network/process/filesystem capabilities are explicit metadata.
