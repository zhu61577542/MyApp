package input

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	WireSize      = 32
	WireVersion   = 1
	MaxCoordinate = 1_000_000
	MaxScroll     = 100
)

type Kind byte

const (
	MouseAbsolute Kind = iota + 1
	MouseRelative
	MouseButtonDown
	MouseButtonUp
	MouseScroll
	KeyDown
	KeyUp
	ReleaseAll
)

type Event struct {
	Kind     Kind
	Sequence uint64
	X        int32
	Y        int32
	Code     uint16
	Value    int32
}

func (e Event) Validate() error {
	if e.Sequence == 0 {
		return errors.New("输入事件序号必须大于零")
	}
	switch e.Kind {
	case MouseAbsolute:
		if e.X < 0 || e.Y < 0 || e.X > MaxCoordinate || e.Y > MaxCoordinate || e.Code != 0 || e.Value != 0 {
			return errors.New("绝对鼠标事件无效")
		}
	case MouseRelative:
		if e.Code != 0 || e.Value != 0 {
			return errors.New("相对鼠标事件无效")
		}
	case MouseButtonDown, MouseButtonUp:
		if e.Code < 1 || e.Code > 5 || e.X != 0 || e.Y != 0 || e.Value != 0 {
			return errors.New("鼠标按钮事件无效")
		}
	case MouseScroll:
		if e.Code != 0 || e.Value != 0 || e.X == 0 && e.Y == 0 || e.X < -MaxScroll || e.X > MaxScroll || e.Y < -MaxScroll || e.Y > MaxScroll {
			return errors.New("滚轮事件无效")
		}
	case KeyDown, KeyUp:
		if e.Code < 4 || e.Code > 231 || e.X != 0 || e.Y != 0 || e.Value != 0 {
			return errors.New("键盘 HID 用法无效")
		}
	case ReleaseAll:
		if e.X != 0 || e.Y != 0 || e.Code != 0 || e.Value != 0 {
			return errors.New("释放事件不能携带数据")
		}
	default:
		return fmt.Errorf("未知输入事件类型: %d", e.Kind)
	}
	return nil
}

type Injector interface {
	Inject(Event) error
	ReleaseAll() error
	Close() error
}

type Capturer interface {
	Capture(context.Context, func(Event) error) error
	Close() error
}

func (e Event) Encode(writer io.Writer) error {
	if err := e.Validate(); err != nil {
		return err
	}
	data := make([]byte, WireSize)
	data[0], data[1] = WireVersion, byte(e.Kind)
	binary.BigEndian.PutUint64(data[4:12], e.Sequence)
	binary.BigEndian.PutUint32(data[12:16], uint32(e.X))
	binary.BigEndian.PutUint32(data[16:20], uint32(e.Y))
	binary.BigEndian.PutUint16(data[20:22], e.Code)
	binary.BigEndian.PutUint32(data[24:28], uint32(e.Value))
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func Decode(reader io.Reader) (Event, error) {
	data := make([]byte, WireSize)
	if _, err := io.ReadFull(reader, data); err != nil {
		return Event{}, err
	}
	if data[0] != WireVersion || data[2] != 0 || data[3] != 0 || data[22] != 0 || data[23] != 0 || binary.BigEndian.Uint32(data[28:32]) != 0 {
		return Event{}, errors.New("输入事件版本或保留字段无效")
	}
	event := Event{
		Kind:     Kind(data[1]),
		Sequence: binary.BigEndian.Uint64(data[4:12]),
		X:        int32(binary.BigEndian.Uint32(data[12:16])),
		Y:        int32(binary.BigEndian.Uint32(data[16:20])),
		Code:     binary.BigEndian.Uint16(data[20:22]),
		Value:    int32(binary.BigEndian.Uint32(data[24:28])),
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func Coalesce(events []Event) []Event {
	result := make([]Event, 0, len(events))
	for _, event := range events {
		if len(result) > 0 && event.Kind == MouseRelative && result[len(result)-1].Kind == MouseRelative {
			previous := &result[len(result)-1]
			previous.X = saturatingAdd(previous.X, event.X)
			previous.Y = saturatingAdd(previous.Y, event.Y)
			previous.Sequence = event.Sequence
			continue
		}
		if len(result) > 0 && event.Kind == MouseAbsolute && result[len(result)-1].Kind == MouseAbsolute {
			result[len(result)-1] = event
			continue
		}
		result = append(result, event)
	}
	return result
}

func saturatingAdd(first, second int32) int32 {
	value := int64(first) + int64(second)
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}
