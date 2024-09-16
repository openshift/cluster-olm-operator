package utils

import (
	"encoding/json"
	"fmt"
	"strings"

	semver "github.com/blang/semver/v4"
)

func GetNextOCPMinorVersion(versionString string) (*semver.Version, error) {
	v, err := semver.Parse(versionString)
	if err != nil {
		return &v, err
	}
	v.Build = nil                 // Builds are irrelevant
	v.Pre = nil                   // Next Y release
	return &v, v.IncrementMinor() // Sets Y=Y+1 and Z=0
}

func ToAllowedSemver(data []byte) (*semver.Version, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var versionStr string

	switch v := raw.(type) {
	case float64:
		versionStr = fmt.Sprintf("%d.%d", int(v), int((v-float64(int(v)))*100)+1)
	case string:
		versionStr = v
	default:
		return nil, fmt.Errorf("invalid type %T for olm.maxOpenshiftVersion: %s", v, string(data))
	}

	if !strings.Contains(versionStr, ".") || strings.Count(versionStr, ".") != 1 {
		return nil, fmt.Errorf("invalid version format")
	}

	if strings.HasPrefix(versionStr, "v") || strings.Count(versionStr, ".") != 1 {
		return nil, fmt.Errorf("invalid version format")
	}

	// So it accepts only Major.Minor without Patch
	version, err := semver.ParseTolerant(versionStr)
	if err != nil {
		return nil, err
	}

	return &version, nil
}
