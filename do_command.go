package hockeyplayer

import (
	"context"
	"fmt"
	"math"
)

type driveMode int

const (
	modeRPM   driveMode = iota // default
	modePower
)

type drive struct {
	mode          driveMode
	rpm           float64 // populated when mode == modeRPM
	power         float64 // populated when mode == modePower (magnitude in (0, 1])
	speedMMPerSec float64
}

func (s *hockeyPlayerHockeyPlayer) parseDrive(cmd map[string]interface{}) (drive, error) {
	rpmVal, rpmOK, err := optFloat(cmd, "rpm")
	if err != nil {
		return drive{}, err
	}
	pwrVal, pwrOK, err := optFloat(cmd, "power")
	if err != nil {
		return drive{}, err
	}
	if rpmOK && pwrOK {
		return drive{}, fmt.Errorf("cannot specify both 'rpm' and 'power'")
	}

	speedVal, speedOK, err := optFloat(cmd, "speed_mm_per_sec")
	if err != nil {
		return drive{}, err
	}
	if speedOK && speedVal <= 0 {
		return drive{}, fmt.Errorf("'speed_mm_per_sec' must be > 0, got %v", speedVal)
	}
	speed := s.cfg.DefaultSpeedMMPerSec
	if speedOK {
		speed = speedVal
	}

	if pwrOK {
		if pwrVal <= 0 || pwrVal > 1 {
			return drive{}, fmt.Errorf("'power' must be in (0, 1], got %v", pwrVal)
		}
		return drive{mode: modePower, power: pwrVal, speedMMPerSec: speed}, nil
	}
	rpm := s.cfg.DefaultRPMRotation
	if rpmOK {
		if rpmVal <= 0 {
			return drive{}, fmt.Errorf("'rpm' must be > 0, got %v", rpmVal)
		}
		rpm = rpmVal
	}
	return drive{mode: modeRPM, rpm: rpm, speedMMPerSec: speed}, nil
}

type axisJob struct {
	name  string // "rotation" or "translation"
	errCh <-chan error
}

func (s *hockeyPlayerHockeyPlayer) doMotion(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	tVal, tOK, err := optFloat(cmd, "t")
	if err != nil {
		return nil, err
	}
	rVal, rOK, err := optFloat(cmd, "r")
	if err != nil {
		return nil, err
	}
	if !tOK && !rOK {
		return nil, fmt.Errorf("no motion fields provided (need 't' and/or 'r')")
	}
	if tOK && (tVal < 0 || tVal > 1) {
		return nil, fmt.Errorf("'t' must be in [0, 1], got %v", tVal)
	}
	if tOK {
		tVal = applyTInvert(tVal, s.cfg.Invert)
	}
	if rOK && (rVal < 0 || rVal > 360) {
		return nil, fmt.Errorf("'r' must be in [0, 360], got %v", rVal)
	}

	dirStr, dirOK, err := optString(cmd, "direction")
	if err != nil {
		return nil, err
	}
	if dirOK && dirStr != "cw" && dirStr != "ccw" {
		return nil, fmt.Errorf("'direction' must be \"cw\" or \"ccw\", got %q", dirStr)
	}
	dirSource := s.cfg.DefaultDirection
	if dirOK {
		dirSource = dirStr
	}
	var dir rotationDirection
	switch dirSource {
	case "cw":
		dir = directionClockwise
	case "ccw":
		dir = directionCounterClockwise
	default:
		dir = directionShortest
	}
	if s.cfg.InvertSpin {
		if dir == directionClockwise {
			dir = directionCounterClockwise
		} else if dir == directionCounterClockwise {
			dir = directionClockwise
		}
	}

	drv, err := s.parseDrive(cmd)
	if err != nil {
		return nil, err
	}

	jobs := make([]axisJob, 0, 2)

	if rOK {
		posRev, err := s.rotationMotor.Position(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("reading rotation motor position: %w", err)
		}
		currentDeg := normalizeAngle(posRev)
		delta := computeDelta(currentDeg, rVal, dir)
		if math.Abs(delta) < 0.01 {
			s.logger.Debugf("rotation already at target (current=%.2f°, target=%.2f°)", currentDeg, rVal)
			// No dispatch; axis simply absent from jobs.
		} else if drv.mode == modeRPM {
			targetRev := posRev + delta/360.0
			motorRef := s.rotationMotor
			rpm := drv.rpm
			ch := s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
				return motorRef.GoTo(c, rpm, targetRev, nil)
			})
			jobs = append(jobs, axisJob{name: "rotation", errCh: ch})
		} else {
			// power mode
			motorRef := s.rotationMotor
			pwr := drv.power
			tol := s.cfg.RotationTolDeg()
			poll := defaultPowerPollInterval
			currentAccumDeg := posRev * 360.0
			targetAccumDeg := currentAccumDeg + delta
			ch := s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
				return powerMoveTo(c, powerMove{
					readPos: func(ctx context.Context) (float64, error) {
						p, err := motorRef.Position(ctx, nil)
						if err != nil {
							return 0, err
						}
						return p * 360.0, nil
					},
					setPower: func(ctx context.Context, p float64) error { return motorRef.SetPower(ctx, p, nil) },
					stop:     func(ctx context.Context) error { return motorRef.Stop(ctx, nil) },
					target:   targetAccumDeg,
					power:    pwr,
					tol:      tol,
					poll:     poll,
				})
			})
			jobs = append(jobs, axisJob{name: "rotation", errCh: ch})
		}
	}

	if tOK {
		if drv.mode == modeRPM {
			currentPositions, err := s.gantry.Position(ctx, nil)
			if err != nil {
				return nil, fmt.Errorf("reading gantry position: %w", err)
			}
			targetMM := tToMM(tVal, s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM)
			positions, err := buildPositions(currentPositions, s.cfg.TranslationAxisIndex, targetMM)
			if err != nil {
				return nil, err
			}
			speeds := make([]float64, len(positions))
			for i := range speeds {
				speeds[i] = drv.speedMMPerSec
			}
			gantryRef := s.gantry
			ch := s.translationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
				return gantryRef.MoveToPosition(c, positions, speeds, nil)
			})
			jobs = append(jobs, axisJob{name: "translation", errCh: ch})
		} else {
			// power mode
			axisIdx := s.cfg.TranslationAxisIndex
			gantryRef := s.gantry
			translationMotorRef := s.translationMotor
			targetMM := tToMM(tVal, s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM)
			pwr := drv.power
			tol := s.cfg.TranslationTolMM()
			poll := defaultPowerPollInterval
			ch := s.translationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
				return powerMoveTo(c, powerMove{
					readPos: func(ctx context.Context) (float64, error) {
						pos, err := gantryRef.Position(ctx, nil)
						if err != nil {
							return 0, err
						}
						if axisIdx >= len(pos) {
							return 0, fmt.Errorf("gantry returned %d axes but translation_axis_index is %d", len(pos), axisIdx)
						}
						return pos[axisIdx], nil
					},
					setPower: func(ctx context.Context, p float64) error { return translationMotorRef.SetPower(ctx, p, nil) },
					stop:     func(ctx context.Context) error { return translationMotorRef.Stop(ctx, nil) },
					target:   targetMM,
					power:    pwr,
					tol:      tol,
					poll:     poll,
				})
			})
			jobs = append(jobs, axisJob{name: "translation", errCh: ch})
		}
	}

	// Wait for all axis goroutines to finish.
	var firstErr error
	remaining := len(jobs)
	for remaining > 0 {
		var rCh, tCh <-chan error
		for i := range jobs {
			if jobs[i].errCh == nil {
				continue
			}
			switch jobs[i].name {
			case "rotation":
				rCh = jobs[i].errCh
			case "translation":
				tCh = jobs[i].errCh
			}
		}

		var finishedName string
		var finishedErr error
		select {
		case <-ctx.Done():
			// Caller cancelled. Preempt every remaining axis so hardware stops.
			s.rotationAxis.close()
			s.translationAxis.close()
			return nil, ctx.Err()
		case finishedErr = <-rCh:
			finishedName = "rotation"
		case finishedErr = <-tCh:
			finishedName = "translation"
		}

		for i := range jobs {
			if jobs[i].name == finishedName {
				jobs[i].errCh = nil
				break
			}
		}
		remaining--
		if finishedErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", finishedName, finishedErr)
			if finishedName == "rotation" {
				s.translationAxis.close()
			} else {
				s.rotationAxis.close()
			}
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}

	// Build final response.
	resp := map[string]interface{}{"status": "done"}

	if tOK {
		gPos, err := s.gantry.Position(ctx, nil)
		if err == nil && s.cfg.TranslationAxisIndex < len(gPos) {
			resp["t_final"] = applyTInvert(mmToT(gPos[s.cfg.TranslationAxisIndex], s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM), s.cfg.Invert)
		}
	}
	if rOK {
		mPos, err := s.rotationMotor.Position(ctx, nil)
		if err == nil {
			resp["r_final"] = normalizeAngle(mPos)
		}
	}
	return resp, nil
}

func (s *hockeyPlayerHockeyPlayer) doGetPosition(ctx context.Context) (map[string]interface{}, error) {
	gPos, err := s.gantry.Position(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading gantry position: %w", err)
	}
	if s.cfg.TranslationAxisIndex >= len(gPos) {
		return nil, fmt.Errorf("gantry position has %d axes but translation_axis_index is %d",
			len(gPos), s.cfg.TranslationAxisIndex)
	}
	t := applyTInvert(mmToT(gPos[s.cfg.TranslationAxisIndex], s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM), s.cfg.Invert)

	mPos, err := s.rotationMotor.Position(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading rotation motor position: %w", err)
	}
	r := normalizeAngle(mPos)

	gMoving, err := s.gantry.IsMoving(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading gantry is_moving: %w", err)
	}
	mMoving, err := s.rotationMotor.IsMoving(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading motor is_moving: %w", err)
	}

	return map[string]interface{}{
		"t":        t,
		"r":        r,
		"t_moving": gMoving,
		"r_moving": mMoving,
	}, nil
}

func optFloat(m map[string]interface{}, key string) (float64, bool, error) {
	v, ok := m[key]
	if !ok {
		return 0, false, nil
	}
	switch n := v.(type) {
	case float64:
		return n, true, nil
	case float32:
		return float64(n), true, nil
	case int:
		return float64(n), true, nil
	case int64:
		return float64(n), true, nil
	default:
		return 0, false, fmt.Errorf("%q must be a number, got %T", key, v)
	}
}

func optString(m map[string]interface{}, key string) (string, bool, error) {
	v, ok := m[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("%q must be a string, got %T", key, v)
	}
	return s, true, nil
}
