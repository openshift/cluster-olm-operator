package utils

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		want      *semver.Version
		wantErr   bool
	}{
		{
			name:      "valid float version",
			jsonInput: `4.18`,
			want:      &semver.Version{Major: 4, Minor: 18, Patch: 0},
			wantErr:   false,
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
