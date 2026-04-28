// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package version

import "strings"

// BuildVersion is set at link-time using -ldflags "-X masterdns2udp/internal/version.BuildVersion=..."
var BuildVersion = "dev"

// GetVersion returns the current build version.
func GetVersion() string {
	v := strings.TrimSpace(BuildVersion)
	if v == "" {
		return "dev"
	}
	return v
}
