package versionutils

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
)

func TestToAllowedSemver(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		want      *semver.Version
		wantErr   bool
	}{
		{
			name:      "valid float version 4.18",
			jsonInput: `4.18`,
			want:      &semver.Version{Major: 4, Minor: 18, Patch: 0},
			wantErr:   false,
		},
		{
			name:      "valid float version 4.19",
			jsonInput: `4.19`,
			want:      &semver.Version{Major: 4, Minor: 19, Patch: 0},
			wantErr:   false,
		},
		{
			name:      "invalid float version 4.19e1",
			jsonInput: `4.19e1`,
			wantErr:   true,
		},
		{
			name:      "valid string version",
			jsonInput: `"4.18"`,
			want:      &semver.Version{Major: 4, Minor: 18, Patch: 0},
			wantErr:   false,
		},
		{
			name:      "invalid float version with patch",
			jsonInput: `4.18.0`,
			wantErr:   true,
		},
		{
			name:      "invalid string version with patch",
			jsonInput: `"4.18.0"`,
			wantErr:   true,
		},
		{
			name:      "invalid string with v prefix",
			jsonInput: `"v4.18"`,
			wantErr:   true,
		},
		{
			name:      "invalid string with v prefix and patch",
			jsonInput: `"v4.18.0"`,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToAllowedSemver([]byte(tt.jsonInput))
			if tt.wantErr {
				assert.Error(t, err, "expected an error but got none")
			} else {
				assert.NoError(t, err, "expected no error but got one")
				assert.Equal(t, tt.want, got, "unexpected semver version")
			}
		})
	}
}

func TestIsOperatorMaxOCPVersionCompatibleWithCluster(t *testing.T) {
	tests := []struct {
		name                   string
		operatorMaxOCPVersion  semver.Version
		currentOCPMinorVersion semver.Version
		want                   bool
	}{
		{
			name:                   "maxOCPVersion is 4.18, currentOCPMinorVersion is 4.17 => compatible",
			operatorMaxOCPVersion:  semver.Version{Major: 4, Minor: 18, Patch: 0},
			currentOCPMinorVersion: semver.Version{Major: 4, Minor: 17, Patch: 0},
			want:                   true,
		},
		{
			name:                   "maxOCPVersion is 4.18, currentOCPMinorVersion is 4.18 => incompatible",
			operatorMaxOCPVersion:  semver.Version{Major: 4, Minor: 18, Patch: 0},
			currentOCPMinorVersion: semver.Version{Major: 4, Minor: 18, Patch: 0},
			want:                   false,
		},
		{
			name:                   "maxOCPVersion is 4.18, currentOCPMinorVersion is 4.19 => incompatible",
			operatorMaxOCPVersion:  semver.Version{Major: 4, Minor: 18, Patch: 0},
			currentOCPMinorVersion: semver.Version{Major: 4, Minor: 19, Patch: 0},
			want:                   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsOperatorMaxOCPVersionCompatibleWithCluster(tt.operatorMaxOCPVersion, tt.currentOCPMinorVersion)
			assert.Equal(t, tt.want, got)
		})
	}
}
