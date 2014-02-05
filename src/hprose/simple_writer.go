/**********************************************************\
|                                                          |
|                          hprose                          |
|                                                          |
| Official WebSite: http://www.hprose.com/                 |
|                   http://www.hprose.net/                 |
|                   http://www.hprose.org/                 |
|                                                          |
\**********************************************************/
/**********************************************************\
 *                                                        *
 * hprose/simple_writer.go                                *
 *                                                        *
 * hprose SimpleWriter for Go.                            *
 *                                                        *
 * LastModified: Feb 4, 2014                              *
 * Author: Ma Bingyao <andot@hprfc.com>                   *
 *                                                        *
\**********************************************************/

package hprose

import (
	"container/list"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
	"uuid"
)

const rune3Max = 1<<16 - 1

var serializeType = [...]bool{
	false, // Invalid
	true,  // Bool
	true,  // Int
	true,  // Int8
	true,  // Int16
	true,  // Int32
	true,  // Int64
	true,  // Uint
	true,  // Uint8
	true,  // Uint16
	true,  // Uint32
	true,  // Uint64
	false, // Uintptr
	true,  // Float32
	true,  // Float64
	false, // Complex64
	false, // Complex128
	true,  // Array
	false, // Chan
	false, // Func
	true,  // Interface
	true,  // Map
	true,  // Ptr
	true,  // Slice
	true,  // String
	true,  // Struct
	false, // UnsafePointer
}

type field struct {
	Name  string
	Index []int
}

type cacheType struct {
	fields            []field
	hasAnonymousField bool
}

var fieldCache struct {
	sync.RWMutex
	cache map[reflect.Type]cacheType
}

type writer struct {
	stream    BufWriter
	classref  map[string]int
	fieldsref [][]field
	writerRefer
	numbuf [20]byte
}

type fakeWriterRefer struct{}

func (r fakeWriterRefer) setRef(interface{}) {}

func (r fakeWriterRefer) writeRef(w *writer, v interface{}) (success bool, err error) {
	return false, nil
}

func (r fakeWriterRefer) resetRef() {}

func NewSimpleWriter(stream BufWriter) Writer {
	return &writer{
		stream:      stream,
		writerRefer: fakeWriterRefer{},
	}
}

func (w *writer) Stream() BufWriter {
	return w.stream
}

func (w *writer) Serialize(v interface{}) (err error) {
	return w.fastSerialize(v, reflect.ValueOf(v), 0)
}

func (w *writer) WriteValue(v reflect.Value) (err error) {
	return w.fastSerialize(v.Interface(), v, 0)
}

func (w *writer) WriteNull() error {
	return w.stream.WriteByte(TagNull)
}

func (w *writer) WriteInt64(v int64) (err error) {
	s := w.Stream()
	if v >= 0 && v <= 9 {
		err = s.WriteByte(byte(v + '0'))
	} else {
		if v >= math.MinInt32 && v <= math.MaxInt32 {
			err = s.WriteByte(TagInteger)
		} else {
			err = s.WriteByte(TagLong)
		}
		if err == nil {
			if err = w.writeInt64(v); err == nil {
				err = s.WriteByte(TagSemicolon)
			}
		}
	}
	return err
}

func (w *writer) WriteUint64(v uint64) (err error) {
	s := w.Stream()
	if v >= 0 && v <= 9 {
		err = s.WriteByte(byte(v + '0'))
	} else {
		if v <= math.MaxInt32 {
			err = s.WriteByte(TagInteger)
		} else {
			err = s.WriteByte(TagLong)
		}
		if err == nil {
			if err = w.writeUint64(v); err == nil {
				err = s.WriteByte(TagSemicolon)
			}
		}
	}
	return err
}

func (w *writer) WriteBigInt(v *big.Int) (err error) {
	s := w.stream
	if err = s.WriteByte(TagLong); err == nil {
		if _, err = s.WriteString(v.String()); err == nil {
			err = s.WriteByte(TagSemicolon)
		}
	}
	return err
}

func (w *writer) WriteFloat64(v float64) (err error) {
	s := w.stream
	if math.IsNaN(v) {
		return w.stream.WriteByte(TagNaN)
	} else if math.IsInf(v, 0) {
		if err = s.WriteByte(TagInfinity); err == nil {
			if v > 0 {
				err = s.WriteByte(TagPos)
			} else {
				err = s.WriteByte(TagNeg)
			}
		}
	} else if err = s.WriteByte(TagDouble); err == nil {
		if _, err = s.WriteString(strconv.FormatFloat(v, 'g', -1, 64)); err == nil {
			err = s.WriteByte(TagSemicolon)
		}
	}
	return err
}

func (w *writer) WriteBool(v bool) error {
	s := w.stream
	if v {
		return s.WriteByte(TagTrue)
	}
	return s.WriteByte(TagFalse)
}

func (w *writer) WriteTime(t time.Time) (err error) {
	return w.writeTime(t, t)
}

func (w *writer) WriteString(str string) (err error) {
	return w.writeString(str, str)
}

func (w *writer) WriteStringWithRef(str string) (err error) {
	s := w.stream
	if length := len(str); length == 0 {
		err = s.WriteByte(TagEmpty)
	} else if length < utf8.UTFMax && utf8.RuneCountInString(str) == 1 {
		if err = s.WriteByte(TagUTF8Char); err == nil {
			_, err = s.WriteString(str)
		}
	} else {
		err = w.writeStringWithRef(str, str)
	}
	return err
}

func (w *writer) WriteBytes(bytes []byte) (err error) {
	return w.writeBytes(&bytes, bytes)
}

func (w *writer) WriteBytesWithRef(bytes []byte) (err error) {
	s := w.stream
	if length := len(bytes); length == 0 {
		err = s.WriteByte(TagEmpty)
	} else {
		err = w.writeBytesWithRef(&bytes, bytes)
	}
	return err
}

func (w *writer) WriteArray(v []reflect.Value) (err error) {
	w.setRef(&v)
	s := w.stream
	count := len(v)
	if err = s.WriteByte(TagList); err == nil {
		if count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteValue(v[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) Reset() {
	if w.classref != nil {
		w.classref = nil
		w.fieldsref = nil
	}
	w.resetRef()
}

// private methods

func (w *writer) fastSerialize(v interface{}, rv reflect.Value, n int) error {
	switch v := v.(type) {
	case nil:
		return w.WriteNull()
	case int:
		return w.WriteInt64(int64(v))
	case *int:
		return w.WriteInt64(int64(*v))
	case int8:
		return w.WriteInt64(int64(v))
	case *int8:
		return w.WriteInt64(int64(*v))
	case int16:
		return w.WriteInt64(int64(v))
	case *int16:
		return w.WriteInt64(int64(*v))
	case int32:
		return w.WriteInt64(int64(v))
	case *int32:
		return w.WriteInt64(int64(*v))
	case int64:
		return w.WriteInt64(v)
	case *int64:
		return w.WriteInt64(*v)
	case uint:
		return w.WriteUint64(uint64(v))
	case *uint:
		return w.WriteUint64(uint64(*v))
	case uint8:
		return w.WriteUint64(uint64(v))
	case *uint8:
		return w.WriteUint64(uint64(*v))
	case uint16:
		return w.WriteUint64(uint64(v))
	case *uint16:
		return w.WriteUint64(uint64(*v))
	case uint32:
		return w.WriteUint64(uint64(v))
	case *uint32:
		return w.WriteUint64(uint64(*v))
	case uint64:
		return w.WriteUint64(v)
	case *uint64:
		return w.WriteUint64(*v)
	case float32:
		return w.WriteFloat64(float64(v))
	case *float32:
		return w.WriteFloat64(float64(*v))
	case float64:
		return w.WriteFloat64(v)
	case *float64:
		return w.WriteFloat64(*v)
	case bool:
		return w.WriteBool(v)
	case *bool:
		return w.WriteBool(*v)
	case big.Int:
		return w.WriteBigInt(&v)
	case *big.Int:
		return w.WriteBigInt(v)
	case string:
		return w.WriteStringWithRef(v)
	case *string:
		return w.writeStringWithRef(v, *v)
	case time.Time:
		return w.writeTimeWithRef(v, v)
	case *time.Time:
		return w.writeTimeWithRef(v, *v)
	case uuid.UUID:
		return w.writeUUIDWithRef(&v, v)
	case *uuid.UUID:
		return w.writeUUIDWithRef(v, *v)
	case list.List:
		return w.writeListWithRef(&v, &v)
	case *list.List:
		return w.writeListWithRef(v, v)
	case []byte:
		return w.WriteBytesWithRef(v)
	case *[]byte:
		return w.writeBytesWithRef(v, *v)
	case []int:
		return w.writeIntSliceWithRef(&v, v)
	case *[]int:
		return w.writeIntSliceWithRef(v, *v)
	case []int8:
		return w.writeInt8SliceWithRef(&v, v)
	case *[]int8:
		return w.writeInt8SliceWithRef(v, *v)
	case []int16:
		return w.writeInt16SliceWithRef(&v, v)
	case *[]int16:
		return w.writeInt16SliceWithRef(v, *v)
	case []int32:
		return w.writeInt32SliceWithRef(&v, v)
	case *[]int32:
		return w.writeInt32SliceWithRef(v, *v)
	case []int64:
		return w.writeInt64SliceWithRef(&v, v)
	case *[]int64:
		return w.writeInt64SliceWithRef(v, *v)
	case []uint:
		return w.writeUintSliceWithRef(&v, v)
	case *[]uint:
		return w.writeUintSliceWithRef(v, *v)
	case []uint16:
		return w.writeUint16SliceWithRef(&v, v)
	case *[]uint16:
		return w.writeUint16SliceWithRef(v, *v)
	case []uint32:
		return w.writeUint32SliceWithRef(&v, v)
	case *[]uint32:
		return w.writeUint32SliceWithRef(v, *v)
	case []uint64:
		return w.writeUint64SliceWithRef(&v, v)
	case *[]uint64:
		return w.writeUint64SliceWithRef(v, *v)
	case []float32:
		return w.writeFloat32SliceWithRef(&v, v)
	case *[]float32:
		return w.writeFloat32SliceWithRef(v, *v)
	case []float64:
		return w.writeFloat64SliceWithRef(&v, v)
	case *[]float64:
		return w.writeFloat64SliceWithRef(v, *v)
	case []bool:
		return w.writeBoolSliceWithRef(&v, v)
	case *[]bool:
		return w.writeBoolSliceWithRef(v, *v)
	case []string:
		return w.writeStringSliceWithRef(&v, v)
	case *[]string:
		return w.writeStringSliceWithRef(v, *v)
	case []interface{}:
		return w.writeObjectSliceWithRef(&v, v)
	case *[]interface{}:
		return w.writeObjectSliceWithRef(v, *v)
	case map[string]string:
		return w.writeStringMapWithRef(&v, v)
	case *map[string]string:
		return w.writeStringMapWithRef(v, *v)
	case map[string]interface{}:
		return w.writeStrObjMapWithRef(&v, v)
	case *map[string]interface{}:
		return w.writeStrObjMapWithRef(v, *v)
	case map[interface{}]interface{}:
		return w.writeObjectMapWithRef(&v, v)
	case *map[interface{}]interface{}:
		return w.writeObjectMapWithRef(v, *v)
	}
	return w.slowSerialize(v, rv, n)
}

func (w *writer) slowSerialize(v interface{}, rv reflect.Value, n int) error {
	kind := rv.Type().Kind()
	switch kind {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return w.WriteNull()
		}
		return w.slowSerialize(v, rv.Elem(), n+1)
	case reflect.Struct:
		switch x := rv.Interface().(type) {
		case big.Int:
			return w.WriteBigInt(&x)
		case time.Time:
			return w.writeTimeWithRef(v, x)
		case list.List:
			return w.writeListWithRef(v, &x)
		default:
			if n == 0 {
				v = &v
			}
			return w.writeObjectWithRef(v, rv)
		}
	case reflect.Map:
		if rv.IsNil() {
			return w.WriteNull()
		}
		if n == 0 {
			v = &v
		}
		return w.writeMapWithRef(v, rv)
	case reflect.Slice:
		if rv.IsNil() {
			return w.WriteNull()
		} else {
			switch x := rv.Interface().(type) {
			case []byte:
				return w.writeBytesWithRef(v, x)
			case uuid.UUID:
				return w.writeUUIDWithRef(v, x)
			default:
				if n == 0 {
					v = &v
				}
				return w.writeSliceWithRef(v, rv)
			}
		}
	case reflect.Array:
		if n == 0 {
			v = &v
		}
		return w.writeSliceWithRef(v, rv)
	case reflect.String:
		return w.writeStringWithRef(v, rv.String())
	case reflect.Bool:
		return w.WriteBool(rv.Bool())
	case reflect.Float32, reflect.Float64:
		return w.WriteFloat64(rv.Float())
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
		return w.WriteUint64(rv.Uint())
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		return w.WriteInt64(rv.Int())
	default:
		return errors.New("This type is not supported: " + reflect.TypeOf(v).String())
	}
}

func (w *writer) writeTime(v interface{}, t time.Time) (err error) {
	w.setRef(v)
	s := w.stream
	year, month, day := t.Date()
	hour, min, sec := t.Clock()
	nsec := t.Nanosecond()
	tag := TagSemicolon
	if t.Location() == time.UTC {
		tag = TagUTC
	}
	if hour == 0 && min == 0 && sec == 0 && nsec == 0 {
		if _, err = s.Write(formatDate(year, int(month), day)); err == nil {
			err = s.WriteByte(tag)
		}
	} else if year == 1 && month == 1 && day == 1 {
		if _, err = s.Write(formatTime(hour, min, sec, nsec)); err == nil {
			err = s.WriteByte(tag)
		}
	} else if _, err = s.Write(formatDate(year, int(month), day)); err == nil {
		if _, err = s.Write(formatTime(hour, min, sec, nsec)); err == nil {
			err = s.WriteByte(tag)
		}
	}
	return err
}

func (w *writer) writeTimeWithRef(v interface{}, t time.Time) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeTime(v, t)
	} else {
		return err
	}
}

func (w *writer) writeString(v interface{}, str string) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagString); err == nil {
		if length := ulen(str); length > 0 {
			if err = w.writeInt(length); err == nil {
				if err = s.WriteByte(TagQuote); err == nil {
					if _, err = s.WriteString(str); err == nil {
						err = s.WriteByte(TagQuote)
					}
				}
			}
		} else if err = s.WriteByte(TagQuote); err == nil {
			err = s.WriteByte(TagQuote)
		}
	}
	return err
}

func (w *writer) writeStringWithRef(v interface{}, str string) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeString(v, str)
	} else {
		return err
	}
}

func (w *writer) writeBytes(v interface{}, bytes []byte) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagBytes); err == nil {
		if length := len(bytes); length > 0 {
			if err = w.writeInt(length); err == nil {
				if err = s.WriteByte(TagQuote); err == nil {
					if _, err = s.Write(bytes); err == nil {
						err = s.WriteByte(TagQuote)
					}
				}
			}
		} else if err = s.WriteByte(TagQuote); err == nil {
			err = s.WriteByte(TagQuote)
		}
	}
	return err
}

func (w *writer) writeBytesWithRef(v interface{}, bytes []byte) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeBytes(v, bytes)
	} else {
		return err
	}
}

func (w *writer) writeUUID(v interface{}, u uuid.UUID) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagGuid); err == nil {
		if err = s.WriteByte(TagOpenbrace); err == nil {
			if _, err = s.WriteString(u.String()); err == nil {
				err = s.WriteByte(TagClosebrace)
			}
		}
	}
	return err
}

func (w *writer) writeUUIDWithRef(v interface{}, u uuid.UUID) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeUUID(v, u)
	} else {
		return err
	}
}

func (w *writer) writeList(v interface{}, l *list.List) (err error) {
	w.setRef(v)
	s := w.stream
	count := l.Len()
	if err = s.WriteByte(TagList); err == nil {
		if count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for e := l.Front(); e != nil; e = e.Next() {
						if err = w.Serialize(e.Value); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeListWithRef(v interface{}, l *list.List) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeList(v, l)
	} else {
		return err
	}
}

func (w *writer) writeIntSlice(v interface{}, a []int) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteInt64(int64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeIntSliceWithRef(v interface{}, a []int) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeIntSlice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeInt8Slice(v interface{}, a []int8) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteInt64(int64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeInt8SliceWithRef(v interface{}, a []int8) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeInt8Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeInt16Slice(v interface{}, a []int16) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteInt64(int64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeInt16SliceWithRef(v interface{}, a []int16) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeInt16Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeInt32Slice(v interface{}, a []int32) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteInt64(int64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeInt32SliceWithRef(v interface{}, a []int32) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeInt32Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeInt64Slice(v interface{}, a []int64) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteInt64(a[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeInt64SliceWithRef(v interface{}, a []int64) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeInt64Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeUintSlice(v interface{}, a []uint) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteUint64(uint64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeUintSliceWithRef(v interface{}, a []uint) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeUintSlice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeUint16Slice(v interface{}, a []uint16) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteUint64(uint64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeUint16SliceWithRef(v interface{}, a []uint16) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeUint16Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeUint32Slice(v interface{}, a []uint32) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteUint64(uint64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeUint32SliceWithRef(v interface{}, a []uint32) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeUint32Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeUint64Slice(v interface{}, a []uint64) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteUint64(a[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeUint64SliceWithRef(v interface{}, a []uint64) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeUint64Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeFloat32Slice(v interface{}, a []float32) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteFloat64(float64(a[i])); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeFloat32SliceWithRef(v interface{}, a []float32) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeFloat32Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeFloat64Slice(v interface{}, a []float64) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteFloat64(a[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeFloat64SliceWithRef(v interface{}, a []float64) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeFloat64Slice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeBoolSlice(v interface{}, a []bool) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteBool(a[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeBoolSliceWithRef(v interface{}, a []bool) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeBoolSlice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeStringSlice(v interface{}, a []string) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteStringWithRef(a[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeStringSliceWithRef(v interface{}, a []string) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeStringSlice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeObjectSlice(v interface{}, a []interface{}) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := len(a); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.Serialize(a[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeObjectSliceWithRef(v interface{}, a []interface{}) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeObjectSlice(v, a)
	} else {
		return err
	}
}

func (w *writer) writeSlice(v interface{}, rv reflect.Value) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagList); err == nil {
		if count := rv.Len(); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i := 0; i < count; i++ {
						if err = w.WriteValue(rv.Index(i)); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeSliceWithRef(v interface{}, rv reflect.Value) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeSlice(v, rv)
	} else {
		return err
	}
}

func (w *writer) writeStringMap(v interface{}, m map[string]string) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagMap); err == nil {
		if count := len(m); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for k, v := range m {
						if err = w.writeStringWithRef(k, k); err != nil {
							return err
						}
						if err = w.writeStringWithRef(v, v); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeStringMapWithRef(v interface{}, m map[string]string) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeStringMap(v, m)
	} else {
		return err
	}
}

func (w *writer) writeStrObjMap(v interface{}, m map[string]interface{}) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagMap); err == nil {
		if count := len(m); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for k, v := range m {
						if err = w.writeStringWithRef(k, k); err != nil {
							return err
						}
						if err = w.Serialize(v); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeStrObjMapWithRef(v interface{}, m map[string]interface{}) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeStrObjMap(v, m)
	} else {
		return err
	}
}

func (w *writer) writeObjectMap(v interface{}, m map[interface{}]interface{}) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagMap); err == nil {
		if count := len(m); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for k, v := range m {
						if err = w.Serialize(k); err != nil {
							return err
						}
						if err = w.Serialize(v); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeObjectMapWithRef(v interface{}, m map[interface{}]interface{}) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeObjectMap(v, m)
	} else {
		return err
	}
}

func (w *writer) writeMap(v interface{}, rv reflect.Value) (err error) {
	w.setRef(v)
	s := w.stream
	if err = s.WriteByte(TagMap); err == nil {
		if count := rv.Len(); count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					keys := rv.MapKeys()
					for _, key := range keys {
						if err = w.WriteValue(key); err != nil {
							return err
						}
						if err = w.WriteValue(rv.MapIndex(key)); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeMapWithRef(v interface{}, rv reflect.Value) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeMap(v, rv)
	} else {
		return err
	}
}

func (w *writer) writeObjectAsMap(v reflect.Value, fields []field) (err error) {
	s := w.Stream()
	length := len(fields)
	elements := make([]reflect.Value, 0, length)
	names := make([]string, 0, length)
NEXT:
	for i := 0; i < length; i++ {
		f := fields[i]
		e := v.Field(f.Index[0])
		n := len(f.Index)
		if n > 1 {
			for j := 1; j < n; j++ {
				if e.Kind() == reflect.Ptr && e.IsNil() {
					continue NEXT
				}
				e = reflect.Indirect(e).Field(f.Index[j])
			}
		}
		elements = append(elements, e)
		names = append(names, f.Name)
	}
	count := len(elements)
	if err = s.WriteByte(TagMap); err == nil {
		if count > 0 {
			if err = w.writeInt(count); err == nil {
				if err = s.WriteByte(TagOpenbrace); err == nil {
					for i, name := range names {
						if err = w.writeStringWithRef(name, name); err != nil {
							return err
						}
						if err = w.WriteValue(elements[i]); err != nil {
							return err
						}
					}
					err = s.WriteByte(TagClosebrace)
				}
			}
		} else if err = s.WriteByte(TagOpenbrace); err == nil {
			err = s.WriteByte(TagClosebrace)
		}
	}
	return err
}

func (w *writer) writeObject(v interface{}, rv reflect.Value) (err error) {
	s := w.stream
	t := rv.Type()
	classname := ClassManager.GetClassAlias(t)
	if classname == "" {
		classname = t.Name()
		ClassManager.Register(t, classname)
	}
	if w.classref == nil {
		w.classref = make(map[string]int)
		w.fieldsref = make([][]field, 0)
	}
	index, found := w.classref[classname]
	var fields []field
	if found {
		fields = w.fieldsref[index]
	} else {
		fieldCache.RLock()
		cache, found := fieldCache.cache[t]
		fieldCache.RUnlock()
		if !found {
			fieldCache.Lock()
			if fieldCache.cache == nil {
				fieldCache.cache = make(map[reflect.Type]cacheType)
			}
			fields = make([]field, 0)
			hasAnonymousField := false
			getFieldsFunc(t, func(f reflect.StructField) {
				if len(f.Index) > 1 {
					hasAnonymousField = true
				}
				fields = append(fields, field{firstLetterToLower(f.Name), f.Index})
			})
			cache = cacheType{fields, hasAnonymousField}
			fieldCache.cache[t] = cache
			fieldCache.Unlock()
		} else {
			fields = cache.fields
		}
		if !cache.hasAnonymousField {
			if index, err = w.writeClass(classname, fields); err != nil {
				return err
			}
		} else {
			w.setRef(v)
			return w.writeObjectAsMap(rv, fields)
		}
	}
	w.setRef(v)
	if err = s.WriteByte(TagObject); err == nil {
		if err = w.writeInt(index); err == nil {
			if err = s.WriteByte(TagOpenbrace); err == nil {
				for _, f := range fields {
					if err = w.WriteValue(rv.FieldByIndex(f.Index)); err != nil {
						return err
					}
				}
				err = w.stream.WriteByte(TagClosebrace)
			}
		}
	}
	return err
}

func (w *writer) writeObjectWithRef(v interface{}, rv reflect.Value) error {
	if success, err := w.writeRef(w, v); err == nil && !success {
		return w.writeObject(v, rv)
	} else {
		return err
	}
}

func (w *writer) writeClass(classname string, fields []field) (index int, err error) {
	s := w.stream
	count := len(fields)
	if err = s.WriteByte(TagClass); err != nil {
		return -1, err
	}
	if err = w.writeInt(ulen(classname)); err != nil {
		return -1, err
	}
	if err = s.WriteByte(TagQuote); err != nil {
		return -1, err
	}
	if _, err = s.WriteString(classname); err != nil {
		return -1, err
	}
	if err = s.WriteByte(TagQuote); err != nil {
		return -1, err
	}
	if count > 0 {
		if err = w.writeInt(count); err != nil {
			return -1, err
		}
		if err = s.WriteByte(TagOpenbrace); err != nil {
			return -1, err
		}
		for _, f := range fields {
			if err = w.WriteString(f.Name); err != nil {
				return -1, err
			}
		}
		if err = s.WriteByte(TagClosebrace); err != nil {
			return -1, err
		}
	} else {
		if err = s.WriteByte(TagOpenbrace); err != nil {
			return -1, err
		}
		if err = s.WriteByte(TagClosebrace); err != nil {
			return -1, err
		}
	}
	index = len(w.fieldsref)
	w.classref[classname] = index
	w.fieldsref = append(w.fieldsref, fields)
	return index, nil
}

func (w *writer) writeInt64(i int64) error {
	if i >= 0 && i <= 9 {
		return w.stream.WriteByte((byte)(i + '0'))
	} else {
		off := 20
		sign := int64(1)
		if i < 0 {
			sign = -sign
		}
		for i != 0 {
			off--
			w.numbuf[off] = (byte)((i%10)*sign + '0')
			i /= 10
		}
		if sign == -1 {
			off--
			w.numbuf[off] = '-'
		}
		_, err := w.stream.Write(w.numbuf[off:])
		return err
	}
}

func (w *writer) writeUint64(i uint64) error {
	if i >= 0 && i <= 9 {
		return w.stream.WriteByte((byte)(i + '0'))
	} else {
		off := 20
		for i != 0 {
			off--
			w.numbuf[off] = (byte)((i % 10) + '0')
			i /= 10
		}
		_, err := w.stream.Write(w.numbuf[off:])
		return err
	}
}

func (w *writer) writeInt(i int) error {
	return w.writeInt64(int64(i))
}

// private functions

func ulen(str string) (n int) {
	for _, char := range str {
		n++
		if char > rune3Max {
			n++
		}
	}
	return n
}

func formatDate(year int, month int, day int) []byte {
	var date [9]byte
	date[0] = TagDate
	date[1] = byte('0' + (year / 1000 % 10))
	date[2] = byte('0' + (year / 100 % 10))
	date[3] = byte('0' + (year / 10 % 10))
	date[4] = byte('0' + (year % 10))
	date[5] = byte('0' + (month / 10 % 10))
	date[6] = byte('0' + (month % 10))
	date[7] = byte('0' + (day / 10 % 10))
	date[8] = byte('0' + (day % 10))
	return date[:]
}

func formatTime(hour int, min int, sec int, nsec int) []byte {
	var time [7]byte
	time[0] = TagTime
	time[1] = byte('0' + (hour / 10 % 10))
	time[2] = byte('0' + (hour % 10))
	time[3] = byte('0' + (min / 10 % 10))
	time[4] = byte('0' + (min % 10))
	time[5] = byte('0' + (sec / 10 % 10))
	time[6] = byte('0' + (sec % 10))
	if nsec > 0 {
		if nsec%1000000 == 0 {
			var nanoSecond [4]byte
			nanoSecond[0] = TagPoint
			nanoSecond[1] = (byte)('0' + (nsec / 100000000 % 10))
			nanoSecond[2] = (byte)('0' + (nsec / 10000000 % 10))
			nanoSecond[3] = (byte)('0' + (nsec / 1000000 % 10))
			return append(time[:], nanoSecond[:]...)
		} else if nsec%1000 == 0 {
			var nanoSecond [7]byte
			nanoSecond[0] = TagPoint
			nanoSecond[1] = (byte)('0' + (nsec / 100000000 % 10))
			nanoSecond[2] = (byte)('0' + (nsec / 10000000 % 10))
			nanoSecond[3] = (byte)('0' + (nsec / 1000000 % 10))
			nanoSecond[4] = (byte)('0' + (nsec / 100000 % 10))
			nanoSecond[5] = (byte)('0' + (nsec / 10000 % 10))
			nanoSecond[6] = (byte)('0' + (nsec / 1000 % 10))
			return append(time[:], nanoSecond[:]...)

		} else {
			var nanoSecond [10]byte
			nanoSecond[0] = TagPoint
			nanoSecond[1] = (byte)('0' + (nsec / 100000000 % 10))
			nanoSecond[2] = (byte)('0' + (nsec / 10000000 % 10))
			nanoSecond[3] = (byte)('0' + (nsec / 1000000 % 10))
			nanoSecond[4] = (byte)('0' + (nsec / 100000 % 10))
			nanoSecond[5] = (byte)('0' + (nsec / 10000 % 10))
			nanoSecond[6] = (byte)('0' + (nsec / 1000 % 10))
			nanoSecond[7] = (byte)('0' + (nsec / 100 % 10))
			nanoSecond[8] = (byte)('0' + (nsec / 10 % 10))
			nanoSecond[9] = (byte)('0' + (nsec % 10))
			return append(time[:], nanoSecond[:]...)
		}
	}
	return time[:]
}

func firstLetterToLower(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	b := ([]byte)(s)
	b[0] = b[0] - 'A' + 'a'
	return string(b)
}

func getFieldsFunc(class reflect.Type, set func(reflect.StructField)) {
	count := class.NumField()
	for i := 0; i < count; i++ {
		if f := class.Field(i); serializeType[f.Type.Kind()] {
			if !f.Anonymous {
				b := f.Name[0]
				if 'A' <= b && b <= 'Z' {
					set(f)
				}
			} else {
				ft := f.Type
				if ft.Name() == "" && ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					getAnonymousFieldsFunc(ft, f.Index, set)
				}
			}
		}
	}
}

func getAnonymousFieldsFunc(class reflect.Type, index []int, set func(reflect.StructField)) {
	count := class.NumField()
	for i := 0; i < count; i++ {
		if f := class.Field(i); serializeType[f.Type.Kind()] {
			f.Index = append(index, f.Index[0])
			if !f.Anonymous {
				b := f.Name[0]
				if 'A' <= b && b <= 'Z' {
					set(f)
				}
			} else {
				ft := f.Type
				if ft.Name() == "" && ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					getAnonymousFieldsFunc(ft, f.Index, set)
				}
			}
		}
	}
}
