package input

import "math"

type Scaler struct {
	settings                           Settings
	motionX, motionY, scrollX, scrollY int64
}

func NewScaler(settings Settings) (*Scaler, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &Scaler{settings: settings}, nil
}

// Forward在坐标推进和发送序号分配前调用。
func (s *Scaler) Forward(event Event, emit func(Event) error) error {
	switch event.Kind {
	case MouseRelative:
		event.X = scaleMotion(event.X, s.settings.PointerPercent, &s.motionX)
		event.Y = scaleMotion(event.Y, s.settings.PointerPercent, &s.motionY)
		if event.X == 0 && event.Y == 0 {
			return nil
		}
	case MouseScroll:
		x := scaleUnits(event.X, s.settings.ScrollPercent, &s.scrollX)
		y := scaleUnits(event.Y, s.settings.ScrollPercent, &s.scrollY)
		for x != 0 || y != 0 {
			event.X = int32(max(-MaxScroll, min(MaxScroll, x)))
			event.Y = int32(max(-MaxScroll, min(MaxScroll, y)))
			if err := emit(event); err != nil {
				return err
			}
			x -= int64(event.X)
			y -= int64(event.Y)
		}
		return nil
	}
	return emit(event)
}

func scaleUnits(value int32, percent int, remainder *int64) int64 {
	total := int64(value)*int64(percent) + *remainder
	*remainder = total % 100
	return total / 100
}

func scaleMotion(value int32, percent int, remainder *int64) int32 {
	return int32(max(math.MinInt32, min(math.MaxInt32, scaleUnits(value, percent, remainder))))
}
