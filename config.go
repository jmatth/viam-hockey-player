package hockeyplayer

import "fmt"

type Config struct {
	Gantry                  string  `json:"gantry"`
	RotationMotor           string  `json:"rotation_motor"`
	TranslationMotor        string  `json:"translation_motor"`
	TranslationAxisIndex    int     `json:"translation_axis_index,omitempty"`
	MinTranslationMM        float64 `json:"min_translation_mm"`
	MaxTranslationMM        float64 `json:"max_translation_mm"`
	DefaultRPMRotation      float64 `json:"default_rpm_rotation"`
	DefaultSpeedMMPerSec    float64 `json:"default_speed_mm_per_sec"`
	DefaultDirection        string  `json:"default_direction,omitempty"`
	RotationArrivalTolDeg   float64 `json:"rotation_arrival_tol_deg,omitempty"`
	TranslationArrivalTolMM float64 `json:"translation_arrival_tol_mm,omitempty"`
	Invert                  bool    `json:"invert,omitempty"`
	InvertSpin              bool    `json:"invert_spin,omitempty"`
	InvertDegrees           bool    `json:"invert_degrees,omitempty"`
}

func (c *Config) Validate(path string) ([]string, []string, error) {
	if c.Gantry == "" {
		return nil, nil, fmt.Errorf("%s: 'gantry' is required", path)
	}
	if c.RotationMotor == "" {
		return nil, nil, fmt.Errorf("%s: 'rotation_motor' is required", path)
	}
	if c.TranslationMotor == "" {
		return nil, nil, fmt.Errorf("%s: 'translation_motor' is required", path)
	}
	if c.TranslationAxisIndex < 0 {
		return nil, nil, fmt.Errorf("%s: 'translation_axis_index' must be >= 0", path)
	}
	if c.MinTranslationMM < 0 {
		return nil, nil, fmt.Errorf("%s: 'min_translation_mm' must be >= 0", path)
	}
	if c.MaxTranslationMM <= c.MinTranslationMM {
		return nil, nil, fmt.Errorf("%s: 'max_translation_mm' must be > 'min_translation_mm'", path)
	}
	if c.DefaultRPMRotation <= 0 {
		return nil, nil, fmt.Errorf("%s: 'default_rpm_rotation' must be > 0", path)
	}
	if c.DefaultSpeedMMPerSec <= 0 {
		return nil, nil, fmt.Errorf("%s: 'default_speed_mm_per_sec' must be > 0", path)
	}
	if c.RotationArrivalTolDeg < 0 {
		return nil, nil, fmt.Errorf("%s: 'rotation_arrival_tol_deg' must not be negative", path)
	}
	if c.TranslationArrivalTolMM < 0 {
		return nil, nil, fmt.Errorf("%s: 'translation_arrival_tol_mm' must not be negative", path)
	}
	if c.DefaultDirection != "" && c.DefaultDirection != "cw" && c.DefaultDirection != "ccw" {
		return nil, nil, fmt.Errorf("%s: 'default_direction' must be \"cw\", \"ccw\", or omitted", path)
	}
	return []string{c.Gantry, c.RotationMotor, c.TranslationMotor}, nil, nil
}

const (
	defaultRotationArrivalTolDeg   = 0.5
	defaultTranslationArrivalTolMM = 2.0
)

// RotationTolDeg returns the rotation arrival tolerance in degrees, using the
// default when the configured value is zero.
func (c *Config) RotationTolDeg() float64 {
	if c.RotationArrivalTolDeg <= 0 {
		return defaultRotationArrivalTolDeg
	}
	return c.RotationArrivalTolDeg
}

// TranslationTolMM returns the translation arrival tolerance in mm, using the
// default when the configured value is zero.
func (c *Config) TranslationTolMM() float64 {
	if c.TranslationArrivalTolMM <= 0 {
		return defaultTranslationArrivalTolMM
	}
	return c.TranslationArrivalTolMM
}
