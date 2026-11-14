## Summary

<!-- Describe the change and its motivation. -->

## Validation

- [ ] `gofmt` produces no changes.
- [ ] `go vet ./...` passes.
- [ ] `go test ./...` passes.
- [ ] `go build ./...` passes.
- [ ] Android/Termux-specific behavior was considered where relevant.

## Security and compatibility

- [ ] No credentials, tokens, or personal device data are included.
- [ ] Command execution and filesystem boundaries remain enforced.
- [ ] MCP tool schemas and transport behavior remain compatible.
- [ ] Documentation and configuration examples match the implementation.

## Independent review

- [ ] Claude review completed, if configured.
- [ ] Codex review completed, if configured.
- [ ] Grok review completed, if configured.
- [ ] Findings were verified against the diff; no AI review was accepted blindly.

See [AI-assisted pull request review](../docs/AI_REVIEW.md) for setup and
the shared review brief.
