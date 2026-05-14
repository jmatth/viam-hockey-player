package hockeyplayer

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/gantry"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/components/motor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

var (
	HockeyPlayer = resource.NewModel("nfranczak", "generic", "hockey-player")
)

func init() {
	resource.RegisterComponent(generic.API, HockeyPlayer,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newHockeyPlayerHockeyPlayer,
		},
	)
}

type hockeyPlayerHockeyPlayer struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    *Config

	gantry           gantry.Gantry
	rotationMotor    motor.Motor
	translationMotor motor.Motor

	cancelCtx  context.Context
	cancelFunc func()

	rotationAxis    *axisController
	translationAxis *axisController
}

func newHockeyPlayerHockeyPlayer(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}
	return NewHockeyPlayer(ctx, deps, rawConf.ResourceName(), conf, logger)
}

func NewHockeyPlayer(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {
	g, err := gantry.FromProvider(deps, conf.Gantry)
	if err != nil {
		return nil, fmt.Errorf("gantry %q: %w", conf.Gantry, err)
	}
	m, err := motor.FromProvider(deps, conf.RotationMotor)
	if err != nil {
		return nil, fmt.Errorf("rotation_motor %q: %w", conf.RotationMotor, err)
	}
	mt, err := motor.FromProvider(deps, conf.TranslationMotor)
	if err != nil {
		return nil, fmt.Errorf("translation_motor %q: %w", conf.TranslationMotor, err)
	}

	lengths, err := g.Lengths(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading gantry lengths: %w", err)
	}
	if conf.TranslationAxisIndex >= len(lengths) {
		return nil, fmt.Errorf("translation_axis_index %d out of range (gantry has %d axes)",
			conf.TranslationAxisIndex, len(lengths))
	}
	if conf.MaxTranslationMM > lengths[conf.TranslationAxisIndex] {
		return nil, fmt.Errorf("max_translation_mm %.2f exceeds gantry axis %d length %.2f",
			conf.MaxTranslationMM, conf.TranslationAxisIndex, lengths[conf.TranslationAxisIndex])
	}

	props, err := m.Properties(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading motor properties: %w", err)
	}
	if !props.PositionReporting {
		return nil, fmt.Errorf("rotation_motor %q does not support position reporting", conf.RotationMotor)
	}

	if tprops, err := mt.Properties(ctx, nil); err != nil {
		logger.Warnf("translation_motor %q: reading properties failed: %v", conf.TranslationMotor, err)
	} else if !tprops.PositionReporting {
		logger.Warnf("translation_motor %q does not report position (this is OK — position comes from the gantry)", conf.TranslationMotor)
	}

	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	s := &hockeyPlayerHockeyPlayer{
		name:             name,
		logger:           logger,
		cfg:              conf,
		gantry:           g,
		rotationMotor:    m,
		translationMotor: mt,
		cancelCtx:        cancelCtx,
		cancelFunc:       cancelFunc,
	}
	s.rotationAxis = newAxisController(
		"rotation",
		func(c context.Context) error { return m.Stop(c, nil) },
		logger,
	)
	s.translationAxis = newAxisController(
		"translation",
		func(c context.Context) error {
			errM := mt.Stop(c, nil)
			errG := g.Stop(c, nil)
			switch {
			case errM != nil && errG != nil:
				return fmt.Errorf("translation stop: motor=%v, gantry=%v", errM, errG)
			case errM != nil:
				return fmt.Errorf("translation_motor stop: %w", errM)
			case errG != nil:
				return fmt.Errorf("gantry stop: %w", errG)
			}
			return nil
		},
		logger,
	)

	logger.Infof("hockey-player started: t=[%.2f,%.2f]mm axis=%d default_rpm=%.1f default_speed=%.1f default_direction=%q invert=%v rot_tol=%.2f° trans_tol=%.2fmm",
		conf.MinTranslationMM, conf.MaxTranslationMM, conf.TranslationAxisIndex,
		conf.DefaultRPMRotation, conf.DefaultSpeedMMPerSec, conf.DefaultDirection,
		conf.Invert,
		conf.RotationTolDeg(), conf.TranslationTolMM())

	return s, nil
}

func (s *hockeyPlayerHockeyPlayer) Name() resource.Name { return s.name }

func (s *hockeyPlayerHockeyPlayer) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if c, ok := cmd["cmd"].(string); ok {
		switch c {
		case "get_position":
			return s.doGetPosition(ctx)
		default:
			return nil, fmt.Errorf("unknown cmd: %q", c)
		}
	}
	return s.doMotion(ctx, cmd)
}

// Status is required by resource.Resource in this RDK version. We return
// an empty map; callers use the get_position DoCommand for actual state.
func (s *hockeyPlayerHockeyPlayer) Status(ctx context.Context) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (s *hockeyPlayerHockeyPlayer) Close(ctx context.Context) error {
	s.cancelFunc()
	s.rotationAxis.close()
	s.translationAxis.close()
	bg := context.Background()
	if err := s.rotationMotor.Stop(bg, nil); err != nil {
		s.logger.Warnf("close: rotation_motor stop: %v", err)
	}
	if err := s.translationMotor.Stop(bg, nil); err != nil {
		s.logger.Warnf("close: translation_motor stop: %v", err)
	}
	if err := s.gantry.Stop(bg, nil); err != nil {
		s.logger.Warnf("close: gantry stop: %v", err)
	}
	return nil
}
