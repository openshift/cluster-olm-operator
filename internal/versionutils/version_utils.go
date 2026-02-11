package versionutils

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	semver "github.com/blang/semver/v4"
)

func GetCurrentOCPMinorVersion(versionString string) (*semver.Version, error) {
	v, err := semver.Parse(versionString)
	if err != nil {
		return &v, err
	}
	v.Patch = 0               // Patch is ignored (olm.maxOpenShiftVersion is <major>.<minor> only)
	v.Pre, v.Build = nil, nil // Prerelease and Builds are ignored
	return &v, nil
}

const (
	majorMinorPattern = `([1-9][0-9]*)\.([1-9][0-9]*)`
)

var (
	majorMinorRegex = regexp.MustCompile("^" + majorMinorPattern + "$")
)

func ToAllowedSemver(data []byte) (*semver.Version, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var versionStr string

	switch v := raw.(type) {
	case float64:
		matches := majorMinorRegex.FindStringSubmatch(string(data))
		if len(matches) != 3 {
			return nil, fmt.Errorf("invalid semver format %q: expected <major>.<minor>", string(data))
		}
		versionStr = fmt.Sprintf("%s.%s", matches[1], matches[2])
	case string:
		versionStr = v
	default:
		return nil, fmt.Errorf("invalid type %T for olm.maxOpenshiftVersion: %s", v, string(data))
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

// IsOperatorMaxOCPVersionCompatibleWithCluster compares the operator's maximum openshift version with the current cluster version
// and returns whether the operator is compatible with the next cluster version. For example,
//
//	if maxOCPVersion is 4.18 and currentOCPMinorVersion is 4.17 => compatible
//	if maxOCPVersion is 4.18 and currentOCPMinorVersion is 4.18 => incompatible
//	if maxOCPVersion is 4.18 and currentOCPMinorVersion is 4.19 => incompatible
func IsOperatorMaxOCPVersionCompatibleWithCluster(operatorMaxOCPVersion, currentOCPMinorVersion semver.Version) bool {
	return operatorMaxOCPVersion.GT(currentOCPMinorVersion)
}
