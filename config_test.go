package hockeyplayer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCfg() *Config {
	return &Config{
		Gantry:                  "g",
		RotationMotor:           "m_rot",
		TranslationMotor:        "m_trans",
		TranslationAxisIndex:    0,
		MinTranslationMM:        10,
		MaxTranslationMM:        100,
		DefaultRPMRotation:      60,
		DefaultSpeedMMPerSec:    50,
		DefaultWrap:             false,
		RotationArrivalTolDeg:   0.5,
		TranslationArrivalTolMM: 2.0,
	}
}

func TestValidate_HappyPath(t *testing.T) {
	req, opt, err := validCfg().Validate("components.0")
	require.NoError(t, err)
	assert.Equal(t, []string{"g", "m_rot", "m_trans"}, req)
	assert.Nil(t, opt)
}

func TestValidate_Failures(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(c *Config)
		match string
	}{
		{"missing gantry", func(c *Config) { c.Gantry = "" }, "gantry"},
		{"missing rotation_motor", func(c *Config) { c.RotationMotor = "" }, "rotation_motor"},
		{"missing translation_motor", func(c *Config) { c.TranslationMotor = "" }, "translation_motor"},
		{"negative axis index", func(c *Config) { c.TranslationAxisIndex = -1 }, "translation_axis_index"},
		{"negative min translation", func(c *Config) { c.MinTranslationMM = -1 }, "min_translation_mm"},
		{"max <= min", func(c *Config) { c.MaxTranslationMM = 10 }, "max_translation_mm"},
		{"zero default rpm", func(c *Config) { c.DefaultRPMRotation = 0 }, "default_rpm_rotation"},
		{"negative default speed", func(c *Config) { c.DefaultSpeedMMPerSec = -1 }, "default_speed_mm_per_sec"},
		{"negative rotation_arrival_tol", func(c *Config) { c.RotationArrivalTolDeg = -0.1 }, "rotation_arrival_tol_deg"},
		{"negative translation_arrival_tol", func(c *Config) { c.TranslationArrivalTolMM = -0.1 }, "translation_arrival_tol_mm"},
		{"unset tolerances are ok", func(c *Config) {
			c.RotationArrivalTolDeg = 0
			c.TranslationArrivalTolMM = 0
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCfg()
			tc.mut(c)
			_, _, err := c.Validate("components.0")
			if tc.match == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.match)
			assert.Contains(t, err.Error(), "components.0")
		})
	}
}
