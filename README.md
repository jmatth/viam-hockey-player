# Module hockey-player

A Viam module that drives a "hockey player" mechanism with one translational axis (via a gantry) and one rotational axis (via a motor). All interaction goes through `DoCommand` on the generic component.

## Models

- [`nfranczak:generic:hockey-player`](nfranczak_generic_hockey-player.md) — see this doc for configuration and attributes.

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

| Field   | Type  | Description                                                              |
|---------|-------|--------------------------------------------------------------------------|
| `power` | float | Open-loop drive magnitude in `(0, 1]`. Mutually exclusive with `rpm`/`speed_mm_per_sec` — selects power mode instead of RPM mode. |
| `wrap`  | bool  | If true, take the shortest angular path to `r`. Defaults from config.    |

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
