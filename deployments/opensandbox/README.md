# Local OpenSandbox profile

Use [the local deployment contract](../../docs/opensandbox-local.md) when preparing
or changing this profile. The Dockerfiles are offline build recipes; binary,
wheel, Debian-package and license inputs must be prepared and verified first.

The two patches apply only to OpenSandbox
`f89eff24bbd15ae1c37a47769f7667849e679be0`. The profile module is copied into the
patched server before rendering its immutable configuration with
`go run ./cmd/opensandbox-profile`.
