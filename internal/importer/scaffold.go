package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// HasProviderConfig reports whether the directory already declares at least
// one provider via a `terraform { required_providers {...} }` block. Used to
// decide whether import needs to scaffold provider configuration.
func HasProviderConfig(dir string) bool {
	rp, err := bootstrap.ReadRequiredProviders(dir)
	return err == nil && len(rp) > 0
}

// scaffoldProviderRoot writes a minimal versions.tf (required_providers) and
// providers.tf (an empty provider block) so an otherwise-empty directory can
// be initialised and queried. It is used only when the directory has no
// provider configuration of its own.
//
// The generated root is a plain Terraform root, not an Atelier wrapper
// (ADR-0027): no module block, no sparse-write semantics. The empty provider
// block relies on the provider's own environment/CLI-based configuration
// (e.g. the juju provider falls back to the active controller); connection
// credentials are the user's responsibility.
func scaffoldProviderRoot(dir, source, version string) error {
	if source == "" {
		return fmt.Errorf("scaffold: provider source is required")
	}
	local := localProviderName(source)

	versionsPath := filepath.Join(dir, wrapper.VersionsTF)
	if err := writeIfAbsent(versionsPath, []byte(versionsTF(local, source, version))); err != nil {
		return err
	}
	providersPath := filepath.Join(dir, wrapper.ProvidersTF)
	if err := writeIfAbsent(providersPath, []byte(providersTF(local))); err != nil {
		return err
	}
	return nil
}

// versionsTF renders a required_providers block. The version constraint is
// omitted when empty, so `terraform init` selects the latest matching release
// from upstream.
func versionsTF(local, source, version string) string {
	var b strings.Builder
	b.WriteString("terraform {\n  required_providers {\n")
	fmt.Fprintf(&b, "    %s = {\n      source = %q\n", local, source)
	if version != "" {
		fmt.Fprintf(&b, "      version = %q\n", version)
	}
	b.WriteString("    }\n  }\n}\n")
	return b.String()
}

// providersTF renders an empty provider block. Connection configuration is
// left to the provider's environment/CLI defaults (see scaffoldProviderRoot).
func providersTF(local string) string {
	return fmt.Sprintf("provider %q {}\n", local)
}

// writeIfAbsent writes data only if path does not already exist, so scaffolding
// never clobbers a file the user may have authored.
func writeIfAbsent(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already present; leave it untouched
	}
	return os.WriteFile(path, data, 0o644)
}
