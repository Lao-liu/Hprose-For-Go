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
 * hprose/service.go                                      *
 *                                                        *
 * hprose service for Go.                                 *
 *                                                        *
 * LastModified: Feb 3, 2014                              *
 * Author: Ma Bingyao <andot@hprfc.com>                   *
 *                                                        *
\**********************************************************/

package hprose

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

type MissingMethod func(name string, args []reflect.Value) (result []reflect.Value)

type ServiceEvent interface {
	OnBeforeInvoke(name string, args []reflect.Value, byref bool)
	OnAfterInvoke(name string, args []reflect.Value, byref bool, result []reflect.Value)
	OnSendError(err error)
}

type Method struct {
	Function   reflect.Value
	ResultMode ResultMode
	SimpleMode bool
}

type Methods struct {
	MethodNames   []string
	RemoteMethods map[string]*Method
}

func NewMethods() *Methods {
	return &Methods{make([]string, 0, 64), make(map[string]*Method)}
}

func (this *Methods) AddFunction(name string, function interface{}, options ...interface{}) {
	if name == "" {
		panic("name can't be empty")
	}
	if function == nil {
		panic("function can't be nil")
	}
	f := reflect.ValueOf(function)
	if f.Kind() != reflect.Func {
		panic("function must be func or bound method")
	}
	count := len(options)
	resultMode := Normal
	simpleMode := false
	prefix := ""
	for i := 0; i < count; i++ {
		switch opt := options[i].(type) {
		case ResultMode:
			resultMode = opt
		case bool:
			simpleMode = opt
		case string:
			prefix = opt
		default:
			panic("unknown options")
		}
	}
	if prefix != "" && name != "*" {
		name = prefix + "_" + name
	}
	this.MethodNames = append(this.MethodNames, name)
	m := &Method{Function: f, ResultMode: resultMode, SimpleMode: simpleMode}
	this.RemoteMethods[strings.ToLower(name)] = m
}

func (this *Methods) AddFunctions(names []string, functions []interface{}, options ...interface{}) {
	if len(names) != len(functions) {
		panic("names and functions must have the same length")
	}
	count := len(names)
	for i := 0; i < count; i++ {
		this.AddFunction(names[i], functions[i], options...)
	}
}

func (this *Methods) AddMethods(obj interface{}, options ...interface{}) {
	if obj == nil {
		panic("obj can't be nil")
	}
	v := reflect.ValueOf(obj)
	t := v.Type()
	n := t.NumMethod()
	for i := 0; i < n; i++ {
		this.AddFunction(t.Method(i).Name, v.Method(i).Interface(), options...)
	}
}

func (this *Methods) AddMissingMethod(method MissingMethod, options ...interface{}) {
	this.AddFunction("*", method, options...)
}

type BaseService struct {
	*Methods
	ServiceEvent
	Filter
	IOError error
}

func NewBaseService() *BaseService {
	return &BaseService{Methods: NewMethods()}
}

func (service *BaseService) responseEnd(ostream io.Writer, buf []byte, err error) {
	defer recover()
	if service.Filter != nil {
		buf = service.OutputFilter(buf)
	}
	if err != nil && service.ServiceEvent != nil {
		service.OnSendError(err)
	}
	if _, err := ostream.Write(buf); err != nil {
		service.IOError = err
	}
}

func (service *BaseService) sendError(ostream io.Writer, err error) {
	defer recover()
	buf := new(bytes.Buffer)
	writer := NewSimpleWriter(buf)
	writer.Stream().WriteByte(TagError)
	writer.WriteString(err.Error())
	writer.Stream().WriteByte(TagEnd)
	service.responseEnd(ostream, buf.Bytes(), err)
}

func (service *BaseService) doInvoke(istream BufReader, ostream io.Writer) (err error) {
	reader := NewReader(istream)
	buf := new(bytes.Buffer)
	for {
		reader.Reset()
		var name string
		name, err = reader.ReadString()
		if err != nil {
			service.IOError = err
			return err
		}
		alias := strings.ToLower(name)
		remoteMethod := service.RemoteMethods[alias]
		count := 0
		var args []reflect.Value
		byref := false
		var tag byte
		if tag, err = reader.CheckTags([]byte{TagList, TagEnd, TagCall}); err != nil {
			service.IOError = err
			return err
		}
		if tag == TagList {
			reader.Reset()
			if count, err = reader.ReadInteger(TagOpenbrace); err != nil {
				service.IOError = err
				return err
			}
			args = make([]reflect.Value, count)
			if remoteMethod == nil {
				for i := 0; i < count; i++ {
					var e interface{}
					args[i] = reflect.ValueOf(&e).Elem()
				}
				if err = reader.ReadArray(args); err != nil {
					service.IOError = err
					return err
				}
			} else {
				ft := remoteMethod.Function.Type()
				n := ft.NumIn()
				if ft.IsVariadic() {
					n--
				}
				if n < count {
					for i := 0; i < n; i++ {
						args[i] = reflect.New(ft.In(i)).Elem()
					}
					if ft.IsVariadic() {
						t := ft.In(n).Elem()
						for i := n; i < count; i++ {
							args[i] = reflect.New(t).Elem()
						}
						if err = reader.ReadArray(args); err != nil {
							service.IOError = err
							return err
						}
					} else {
						for i := n; i < count; i++ {
							var e interface{}
							args[i] = reflect.ValueOf(&e).Elem()
						}
						if err = reader.ReadArray(args); err != nil {
							service.IOError = err
							return err
						}
						args = args[:n]
					}
				} else {
					for i := 0; i < n; i++ {
						args[i] = reflect.New(ft.In(i)).Elem()
					}
					if err = reader.ReadArray(args[0:count]); err != nil {
						service.IOError = err
						return err
					}
				}
			}
			if tag, err = reader.CheckTags([]byte{TagTrue, TagEnd, TagCall}); err != nil {
				service.IOError = err
				return err
			}
			if tag == TagTrue {
				byref = true
				if tag, err = reader.CheckTags([]byte{TagEnd, TagCall}); err != nil {
					service.IOError = err
					return err
				}
			}
		} else {
			args = make([]reflect.Value, 0)
		}
		if service.ServiceEvent != nil {
			service.OnBeforeInvoke(name, args, byref)
		}
		var result []reflect.Value
		if result, err = func() (out []reflect.Value, err error) {
			defer func() {
				if e := recover(); e != nil && err == nil {
					err = fmt.Errorf("%v", e)
				}
			}()
			if remoteMethod == nil {
				remoteMethod = service.RemoteMethods["*"]
				if remoteMethod == nil {
					return nil, errors.New("Can't find this method " + name)
				}
				if missingMethod, ok := remoteMethod.Function.Interface().(MissingMethod); ok {
					return missingMethod(name, args), nil
				} else {
					return nil, errors.New("Can't find this method " + name)
				}
			} else {
				return remoteMethod.Function.Call(args), nil
			}
		}(); err != nil {
			return err
		}
		if service.ServiceEvent != nil {
			service.OnAfterInvoke(name, args, byref, result)
		}
		resultLength := len(result)
		if resultLength > 0 {
			t := remoteMethod.Function.Type().Out(resultLength - 1)
			if t.Implements(reflect.TypeOf(&err).Elem()) {
				if err, ok := result[resultLength-1].Interface().(error); ok {
					return err
				} else {
					resultLength--
					result = result[:resultLength]
				}
			}
		}
		var data []byte
		if remoteMethod.ResultMode != Normal {
			if resultLength == 0 {
				return errors.New("can't find the result value")
			} else {
				switch r := result[0].Interface().(type) {
				case []byte:
					data = r
				case *[]byte:
					data = *r
				case bytes.Buffer:
					data = r.Bytes()
				case *bytes.Buffer:
					data = r.Bytes()
				case string:
					data = []byte(r)
				case *string:
					data = []byte(*r)
				default:
					return errors.New("the result type is wrong")
				}
			}
			if remoteMethod.ResultMode == RawWithEndTag {
				service.responseEnd(ostream, data, nil)
				return nil
			}
		}
		if remoteMethod.ResultMode == Raw {
			buf.Write(data)
		} else {
			var writer Writer
			if remoteMethod.SimpleMode {
				writer = NewSimpleWriter(buf)
			} else {
				writer = NewWriter(buf)
			}
			writer.Stream().WriteByte(TagResult)
			if remoteMethod.ResultMode == Serialized {
				if _, err = writer.Stream().Write(data); err != nil {
					return err
				}
			} else {
				switch resultLength {
				case 0:
					err = writer.Serialize(nil)
				case 1:
					err = writer.WriteValue(result[0])
				default:
					err = writer.WriteArray(result)
				}
				if err != nil {
					return err
				}
			}
			if byref {
				writer.Stream().WriteByte(TagArgument)
				writer.Reset()
				if err = writer.WriteArray(args); err != nil {
					return err
				}
			}
		}
		if tag != TagCall {
			break
		}
	}
	buf.WriteByte(TagEnd)
	service.responseEnd(ostream, buf.Bytes(), nil)
	return nil
}

func (service *BaseService) doFunctionList(ostream io.Writer) error {
	buf := new(bytes.Buffer)
	writer := NewSimpleWriter(buf)
	writer.Stream().WriteByte(TagFunctions)
	if err := writer.Serialize(service.MethodNames); err != nil {
		return err
	}
	writer.Stream().WriteByte(TagEnd)
	service.responseEnd(ostream, buf.Bytes(), nil)
	return nil
}

func (service *BaseService) Handle(istream BufReader, ostream io.Writer) {
	var err error
	defer func() {
		if e := recover(); e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
		if err != nil {
			service.sendError(ostream, err)
		}
	}()
	if service.Filter != nil {
		istream = service.InputFilter(istream)
	}
	buf := []byte{0}
	if _, err = istream.Read(buf); err == nil {
		tag := buf[0]
		switch tag {
		case TagCall:
			err = service.doInvoke(istream, ostream)
		case TagEnd:
			err = service.doFunctionList(ostream)
		default:
			err = errors.New("Unknown Tag: " + string(buf))
		}
	} else {
		service.IOError = err
	}
}
