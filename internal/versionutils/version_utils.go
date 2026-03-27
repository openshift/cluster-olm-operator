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
	majorMinorPattern = `([1-9][0-9]*)\.(0|[1-9][0-9]*)`
)

var (
	majorMinorRegex = regexp.MustCompile("^" + majorMinorPattern + "$")
)

func ToAllowedSemver(data []byte) (*semver.Version, error) {
	var raw any
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

// ocpVersion500 is the semver representation of OCP 5.0, which is co-released with 4.23 as an
// equivalent release. Neither upgrades to the other; both upgrade exclusively to 5.1.
var ocpVersion500 = semver.Version{Major: 5, Minor: 0, Patch: 0}

// IsOperatorMaxOCPVersionCompatibleWithCluster compares the operator's maximum openshift version with the current cluster version
// and returns whether the operator is compatible with the next cluster version. For example,
//
//	if maxOCPVersion is 4.18 and currentOCPMinorVersion is 4.17 => compatible
//	if maxOCPVersion is 4.18 and currentOCPMinorVersion is 4.18 => incompatible
//	if maxOCPVersion is 4.18 and currentOCPMinorVersion is 4.19 => incompatible
//	if maxOCPVersion is 5.0  and currentOCPMinorVersion is 4.23 => incompatible (next upgrade target is 5.1)
//	if maxOCPVersion is 5.1  and currentOCPMinorVersion is 4.23 => compatible
func IsOperatorMaxOCPVersionCompatibleWithCluster(operatorMaxOCPVersion, currentOCPMinorVersion semver.Version) bool {
	effective := currentOCPMinorVersion
	// OCP 4.23 and 5.0 are co-released equivalents; the only upgrade target from either is 5.1.
	// Treat 4.23 as 5.0 so that operators must declare maxOCP > 5.0 (i.e. >= 5.1) to be compatible.
	if currentOCPMinorVersion.Major == 4 && currentOCPMinorVersion.Minor == 23 {
		effective = ocpVersion500
	}
	return operatorMaxOCPVersion.GT(effective)
}
