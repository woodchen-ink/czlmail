// Package rpc 把一个对象的导出方法按名字暴露给 JSON 调用, 语义与 Wails 绑定一致。
//
// iOS 壳里没有 Wails: 前端仍调 window.go.main.App.X(...), 由原生桥把方法名与参数数组
// 交给 Dispatcher。保持同一套语义, 界面代码在两端不用分支:
//   - 参数是 JSON 数组, 按位置解到方法形参; 少传的按零值 (JSON.stringify 会把 undefined 写成 null)
//   - 最后一个返回值是 error 时, 非 nil 即调用失败, 前端 Promise 以错误文本 reject
//   - 其余返回值至多一个, 序列化为 JSON 作为结果
package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

var errorType = reflect.TypeFor[error]()

// Dispatcher 持有接收者的全部导出方法。
type Dispatcher struct {
	methods map[string]reflect.Value
}

// New 收集 receiver (通常是指针) 的导出方法。返回值多于"一个结果 + error"的方法前端无法表达, 直接跳过。
func New(receiver any) *Dispatcher {
	v := reflect.ValueOf(receiver)
	t := v.Type()
	d := &Dispatcher{methods: make(map[string]reflect.Value, t.NumMethod())}
	for i := range t.NumMethod() {
		m := t.Method(i)
		if !m.IsExported() || !supported(m.Type) {
			continue
		}
		d.methods[m.Name] = v.Method(i)
	}
	return d
}

func supported(t reflect.Type) bool {
	// t 含接收者, NumOut 不含。
	switch t.NumOut() {
	case 0, 1:
		return true
	case 2:
		return t.Out(1) == errorType
	default:
		return false
	}
}

// Has 报告方法是否存在, 供测试与诊断使用。
func (d *Dispatcher) Has(name string) bool {
	_, ok := d.methods[name]
	return ok
}

// ErrUnknownMethod 表示没有这个方法 (前端绑定与后端版本不一致)。
var ErrUnknownMethod = errors.New("unknown method")

// Call 调用方法。args 为 JSON 数组 (可为空); 返回结果的 JSON, 无返回值时为 null。
// 方法内的 panic 转成错误返回, 不让一次调用拖垮整个进程。
func (d *Dispatcher) Call(name string, args json.RawMessage) (out json.RawMessage, err error) {
	m, ok := d.methods[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, name)
	}
	in, err := decodeArgs(m.Type(), args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%s: panic: %v", name, r)
		}
	}()
	res := m.Call(in)

	if n := len(res); n > 0 && m.Type().Out(n-1) == errorType {
		if e := res[n-1].Interface(); e != nil {
			return nil, e.(error)
		}
		res = res[:n-1]
	}
	if len(res) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(res[0].Interface())
}

func decodeArgs(t reflect.Type, args json.RawMessage) ([]reflect.Value, error) {
	var raw []json.RawMessage
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &raw); err != nil {
			return nil, fmt.Errorf("arguments must be a JSON array: %w", err)
		}
	}
	if t.IsVariadic() {
		return nil, errors.New("variadic methods are not supported")
	}
	if len(raw) > t.NumIn() {
		return nil, fmt.Errorf("expected %d arguments, got %d", t.NumIn(), len(raw))
	}
	in := make([]reflect.Value, t.NumIn())
	for i := range in {
		p := reflect.New(t.In(i))
		if i < len(raw) && string(raw[i]) != "null" {
			if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
				return nil, fmt.Errorf("argument %d: %w", i, err)
			}
		}
		in[i] = p.Elem()
	}
	return in, nil
}
