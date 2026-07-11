**Atelier has zero Juju-specific logic in its production Go code.** The binary is fully provider-agnostic. All Juju references fall into: test fixtures, documentation/examples, developer workspace files, and inline comments.

No code changes are required to support non-Juju providers. Documentation and test coverage are the only areas that assume Juju.
