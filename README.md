# Module hockey-player

A Viam module that drives a "hockey player" mechanism with one translational axis (via a gantry) and one rotational axis (via a motor). All interaction goes through `DoCommand` on the generic component.

## Model

`viam-rod-hockey:generic:hockey-player`

## Configuration

```json
{
  "gantry": "<gantry-name>",
  "rotation_motor": "<motor-name>",
  "translation_motor": "<motor-name>",
  "min_translation_mm": <float>,
  "max_translation_mm": <float>,
  "default_rpm_rotation": <float>,
  "default_speed_mm_per_sec": <float>,
  "translation_axis_index": <int>,
  "default_direction": "<clockwise|counter-clockwise>",
  "rotation_arrival_tol_deg": <float>,
  "translation_arrival_tol_mm": <float>,
  "invert_movement": <bool>
}
```

### Attributes

| Name                          | Type   | Inclusion | Description |
|-------------------------------|--------|-----------|-------------|
| `gantry`                      | string | Required  | Name of the gantry providing the translational axis. |
| `rotation_motor`              | string | Required  | Name of the motor driving rotation. Must support position reporting. |
| `translation_motor`           | string | Required  | Name of the motor driving the translational axis (used for power-mode control). |
| `min_translation_mm`          | float  | Required  | Minimum translation position in mm. Must be ≥ 0. Maps to `t = 0`. |
| `max_translation_mm`          | float  | Required  | Maximum translation position in mm. Must be > `min_translation_mm` and ≤ the gantry axis length. Maps to `t = 1`. |
| `default_rpm_rotation`        | float  | Required  | Default rotation speed (RPM) when `rpm` is omitted from a move command. Must be > 0. |
| `default_speed_mm_per_sec`    | float  | Required  | Default translation speed (mm/s) when `speed_mm_per_sec` is omitted from a move command. Must be > 0. |
| `translation_axis_index`      | int    | Optional  | Index of the gantry axis used for translation. Defaults to `0`. |
| `default_direction`           | string | Optional  | Default rotation direction: `"cw"` (clockwise) or `"ccw"` (counter-clockwise). Omit to take the shortest angular path. |
| `rotation_arrival_tol_deg`    | float  | Optional  | Arrival tolerance (degrees) for power-mode rotation. Defaults to `0.5`. |
| `translation_arrival_tol_mm`  | float  | Optional  | Arrival tolerance (mm) for power-mode translation. Defaults to `2.0`. |
| `invert_spin`                 | bool   | Optional  | If true, `cw` and `ccw` directions are swapped. Use when the rotation motor is wired backwards. Defaults to `false`. |
| `invert_degrees`              | bool   | Optional  | If true, rotation degrees are mirrored as `360 - r`. Use when one player's 90° is another's 270°. Defaults to `false`. |
| `invert_movement`             | bool   | Optional  | If true, the `t` axis is flipped: user `t = 0` maps to `max_translation_mm` and `t = 1` maps to `min_translation_mm`. `t_final` and `get_position`'s `t` are reported in the same flipped frame. Defaults to `false`. |
| `invert_translation`          | bool   | Optional  | Set when the gantry's homing sequence drives to the **far** end of travel, so raw gantry coordinates are mirrored relative to the frame `min`/`max_translation_mm` were calibrated in. All gantry reads/writes are converted internally (`frame mm = axis length − gantry mm`); `t`, `min`/`max_translation_mm`, and playbooks stay in the calibrated frame. Independent of `invert_movement`. Defaults to `false`. |

### Example Configuration

```json
{
  "gantry": "my-gantry",
  "rotation_motor": "rot-motor",
  "translation_motor": "trans-motor",
  "min_translation_mm": 0,
  "max_translation_mm": 500,
  "default_rpm_rotation": 30,
  "default_speed_mm_per_sec": 100,
  "translation_axis_index": 0,
  "default_direction": "cw"
}
```

## Using DoCommand

`DoCommand` accepts two shapes: a motion command and a position query.

### Move

Send `t` (translation in `[0, 1]`) and/or `r` (rotation in degrees, `[0, 360]`):

```json
{"t": 0.5, "r": 90}
```

#### Speed

Rotation and translation speeds are set independently in the same payload:

| Axis        | Field              | Unit  | Controls                |
|-------------|--------------------|-------|-------------------------|
| Rotation    | `rpm`              | RPM   | Rotation speed          |
| Translation | `speed_mm_per_sec` | mm/s  | Translation speed       |

Example with both speeds specified:

```json
{"t": 0.5, "r": 90, "rpm": 30, "speed_mm_per_sec": 100}
```

Either field can be omitted to use the configured default.

#### Other optional fields

| Field       | Type   | Description                                                              |
|-------------|--------|--------------------------------------------------------------------------|
| `power`     | float  | Open-loop drive magnitude in `(0, 1]`. Mutually exclusive with `rpm`/`speed_mm_per_sec` — selects power mode instead of RPM mode. |
| `direction` | string | Rotation direction: `"cw"` (clockwise) or `"ccw"` (counter-clockwise). Omit to take the shortest angular path. Defaults from config. |

Response:

```json
{"status": "done", "t_final": 0.5, "r_final": 90.0}
```

> ⚠️ Power-mode rotation is incompatible with stepper motors — `SetPower` combined with position polling will hang. Use `rpm` mode for steppers.

### Get position

```json
{"cmd": "get_position"}
```

Response:

```json
{"t": 0.5, "r": 90.0, "t_moving": false, "r_moving": false}
```
