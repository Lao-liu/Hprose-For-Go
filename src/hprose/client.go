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
 * hprose/client.go                                       *
 *                                                        *
 * hprose client for Go.                                  *
 *                                                        *
 * LastModified: Feb 4, 2014                              *
 * Author: Ma Bingyao <andot@hprfc.com>                   *
 *                                                        *
\**********************************************************/

/*

Here is a client example:

	package main

	import (
		"fmt"
		"hprose"
	)

	type testUser struct {
		Name     string
		Sex      int
		Birthday time.Time
		Age      int
		Married  bool
	}

	type testRemoteObject struct {
		Hello               func(string) string
		HelloWithError      func(string) (string, error)               `name:"hello"`
		AsyncHello          func(string) <-chan string                 `name:"hello"`
		AsyncHelloWithError func(string) (<-chan string, <-chan error) `name:"hello"`
		Sum                 func(...int) int
		SwapKeyAndValue     func(*map[string]string) map[string]string `byref:"true"`
		SwapInt             func(int, int) (int, int)                  `name:"swap"`
		SwapFloat           func(float64, float64) (float64, float64)  `name:"swap"`
		Swap                func(interface{}, interface{}) (interface{}, interface{})
		GetUserList         func() []testUser
	}

	func main() {
		client := hprose.NewClient("http://www.hprose.com/example/")
		var ro *RemoteObject
		client.UseService(&ro)

		// If an error occurs, it will panic
		fmt.Println(ro.Hello("World"))

		// If an error occurs, an error value will be returned
		if result, err := ro.HelloWithError("World"); err == nil {
			fmt.Println(result)
		} else {
			fmt.Println(err.Error())
		}

		// If an error occurs, it will be ignored
		result := ro.AsyncHello("World")
		fmt.Println(<-result)

		// If an error occurs, an error chan will be returned
		result, err := ro.AsyncHelloWithError("World")
		if e := <-err; e == nil {
			fmt.Println(<-result)
		} else {
			fmt.Println(e.Error())
		}
		fmt.Println(ro.Sum(1, 2, 3, 4, 5))

		m := make(map[string]string)
		m["Jan"] = "January"
		m["Feb"] = "February"
		m["Mar"] = "March"
		m["Apr"] = "April"
		m["May"] = "May"
		m["Jun"] = "June"
		m["Jul"] = "July"
		m["Aug"] = "August"
		m["Sep"] = "September"
		m["Oct"] = "October"
		m["Nov"] = "November"
		m["Dec"] = "December"

		fmt.Println(m)
		mm := ro.SwapKeyAndValue(&m)
		fmt.Println(m)
		fmt.Println(mm)

		fmt.Println(ro.GetUserList())
		fmt.Println(ro.SwapInt(1, 2))
		fmt.Println(ro.SwapFloat(1.2, 3.4))
		fmt.Println(ro.Swap("Hello", "World"))
	}

*/

package hprose

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

type InvokeOptions struct {
	ByRef      interface{} // true, false, nil
	SimpleMode interface{} // true, false, nil
	ResultMode ResultMode
}

type Client interface {
	UseService(...interface{})
	Invoke(string, []interface{}, *InvokeOptions, interface{}) <-chan error
	Uri() string
	SetUri(string)
}

type Transporter interface {
	GetInvokeContext(uri string) (interface{}, error)
	SendData(context interface{}, data []byte, success bool) error
	GetInputStream(context interface{}) (BufReader, error)
	EndInvoke(context interface{}, success bool) error
}

type BaseClient struct {
	Transporter
	Filter
	ByRef      bool
	SimpleMode bool
	uri        *url.URL
}

var clientFactories = make(map[string]func(string) Client)

func NewBaseClient(trans Transporter) *BaseClient {
	return &BaseClient{Transporter: trans}
}

func NewClient(uri string) Client {
	if u, err := url.Parse(uri); err == nil {
		if newClient, ok := clientFactories[u.Scheme]; ok {
			return newClient(uri)
		}
		panic("The " + u.Scheme + "client isn't implemented.")
	} else {
		panic("The uri can't be parsed.")
	}
}

func (client *BaseClient) Uri() string {
	return client.uri.String()
}

func (client *BaseClient) SetUri(uri string) {
	if u, err := url.Parse(uri); err == nil {
		client.uri = u
	} else {
		panic("The uri can't be parsed.")
	}
}

// UseService(uri string)
// UseService(remoteObject interface{})
// UseService(uri string, remoteObject interface{})
func (client *BaseClient) UseService(args ...interface{}) {
	switch len(args) {
	case 1:
		switch arg0 := args[0].(type) {
		case nil:
			panic("The arguments can't be nil.")
		case string:
			client.SetUri(arg0)
			return
		case *string:
			client.SetUri(*arg0)
			return
		default:
			if isStructPointer(arg0) {
				client.createRemoteObject(arg0)
				return
			}
		}
	case 2:
		switch arg0 := args[0].(type) {
		case nil:
			panic("The arguments can't be nil.")
		case string:
			client.SetUri(arg0)
		case *string:
			client.SetUri(*arg0)
		default:
			panic("Wrong arguments.")
		}
		if args[1] == nil {
			panic("The arguments can't be nil.")
		}
		if isStructPointer(args[1]) {
			client.createRemoteObject(args[1])
		}
	}
	panic("Wrong arguments.")
}

func (client *BaseClient) Invoke(name string, args []interface{}, options *InvokeOptions, result interface{}) <-chan error {
	if result == nil {
		panic("The argument result can't be nil")
	}
	v := reflect.ValueOf(result)
	t := v.Type()
	if t.Kind() != reflect.Ptr {
		panic("The argument result must be pointer type")
	}
	r := []reflect.Value{v.Elem()}
	count := len(args)
	a := make([]reflect.Value, count)
	v = reflect.ValueOf(args)
	for i := 0; i < count; i++ {
		a[i] = v.Index(i).Elem()
	}
	return client.invoke(name, a, options, r)
}

// private methods

func (client *BaseClient) invoke(name string, args []reflect.Value, options *InvokeOptions, result []reflect.Value) <-chan error {
	if options == nil {
		options = new(InvokeOptions)
	}
	length := len(result)
	async := false
	for i := 0; i < length; i++ {
		if result[i].Kind() == reflect.Chan {
			async = true
		} else if async {
			panic("The out parameters must be all chan or all non-chan type.")
		}
	}
	byref := client.ByRef
	if br, ok := options.ByRef.(bool); ok {
		byref = br
	}
	if byref && !checkRefArgs(args) {
		panic("The elements in args must be pointer when options.ByRef is true.")
	}
	if async {
		return client.asyncInvoke(name, args, options, result)
	} else {
		err := make(chan error, 1)
		err <- client.syncInvoke(name, args, options, result)
		return err
	}
}

func (client *BaseClient) syncInvoke(name string, args []reflect.Value, options *InvokeOptions, result []reflect.Value) (err error) {
	context, err := client.GetInvokeContext(client.Uri())
	defer func() {
		if e := recover(); e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
	}()
	if err == nil {
		if err = client.doOutput(context, name, args, options); err == nil {
			err = client.doIntput(context, args, options, result)
		}
	}
	return err
}

func (client *BaseClient) asyncInvoke(name string, args []reflect.Value, options *InvokeOptions, result []reflect.Value) <-chan error {
	length := len(result)
	sender := make([]reflect.Value, length)
	out := make([]reflect.Value, length)
	for i := 0; i < length; i++ {
		t := result[i].Type().Elem()
		out[i] = reflect.New(t).Elem()
		t = reflect.ChanOf(reflect.BothDir, t)
		sender[i] = reflect.MakeChan(t, 1)
		result[i].Set(sender[i])
	}
	errChan := make(chan error, 1)
	go func() {
		err := client.syncInvoke(name, args, options, out)
		for i := 0; i < length; i++ {
			sender[i].Send(out[i])
		}
		errChan <- err
	}()
	return errChan
}

func (client *BaseClient) doOutput(context interface{}, name string, args []reflect.Value, options *InvokeOptions) (err error) {
	success := false
	buf := new(bytes.Buffer)
	defer func() {
		if err == nil {
			data := buf.Bytes()
			if client.Filter != nil {
				data = client.OutputFilter(data)
			}
			err = client.SendData(context, data, success)
		}
	}()
	simple := client.SimpleMode
	if s, ok := options.SimpleMode.(bool); ok {
		simple = s
	}
	byref := client.ByRef
	if br, ok := options.ByRef.(bool); ok {
		byref = br
	}
	var writer Writer
	if simple {
		writer = NewSimpleWriter(buf)
	} else {
		writer = NewWriter(buf)
	}
	if err = writer.Stream().WriteByte(TagCall); err != nil {
		return err
	}
	if err = writer.WriteString(name); err != nil {
		return err
	}
	if args != nil && (len(args) > 0 || byref) {
		writer.Reset()
		if err = writer.WriteArray(args); err != nil {
			return err
		}
		if byref {
			if err = writer.WriteBool(true); err != nil {
				return err
			}
		}
	}
	if err = writer.Stream().WriteByte(TagEnd); err == nil {
		success = true
	}
	return err
}

func (client *BaseClient) doIntput(context interface{}, args []reflect.Value, options *InvokeOptions, result []reflect.Value) (err error) {
	success := true
	defer func() {
		e := client.EndInvoke(context, success)
		if err == nil {
			err = e
		}
	}()
	var istream BufReader
	if istream, err = client.GetInputStream(context); err != nil {
		success = false
		return err
	}
	if client.Filter != nil {
		istream = client.InputFilter(istream)
	}
	resultMode := options.ResultMode
	buf := new(bytes.Buffer)
	reader := NewReader(istream)
	expectTags := []byte{TagResult, TagArgument, TagError, TagEnd}
	var tag byte
	for tag, err = reader.CheckTags(expectTags); err == nil && tag != TagEnd; tag, err = reader.CheckTags(expectTags) {
		switch tag {
		case TagResult:
			switch resultMode {
			case Normal:
				reader.Reset()
				length := len(result)
				if length == 1 {
					err = reader.ReadValue(result[0])
				} else if err = reader.CheckTag(TagList); err == nil {
					var count int
					if count, err = reader.ReadInteger(TagOpenbrace); err == nil {
						r := make([]reflect.Value, count)
						if count <= length {
							for i := 0; i < count; i++ {
								r[i] = result[i]
							}
						} else {
							for i := 0; i < length; i++ {
								r[i] = result[i]
							}
							for i := length; i < count; i++ {
								var e interface{}
								r[i] = reflect.ValueOf(&e).Elem()
							}
						}
						err = reader.ReadArray(r)
					}
				}
				if err != nil {
					success = false
					return err
				}
			case Serialized:
				if err = reader.ReadRawTo(buf); err != nil {
					success = false
					return err
				}
				if err = setResult(result[0], buf); err != nil {
					return err
				}
			default:
				if err = buf.WriteByte(TagResult); err != nil {
					return err
				}
				if err = reader.ReadRawTo(buf); err != nil {
					success = false
					return err
				}
			}
		case TagArgument:
			switch resultMode {
			case Normal, Serialized:
				reader.Reset()
				if err = reader.CheckTag(TagList); err == nil {
					length := len(args)
					var count int
					if count, err = reader.ReadInteger(TagOpenbrace); err == nil {
						a := make([]reflect.Value, count)
						if count <= length {
							for i := 0; i < count; i++ {
								a[i] = args[i].Elem()
							}
						} else {
							for i := 0; i < length; i++ {
								a[i] = args[i].Elem()
							}
							for i := length; i < count; i++ {
								var e interface{}
								a[i] = reflect.ValueOf(&e).Elem()
							}
						}
						err = reader.ReadArray(a)
					}
				}
				if err != nil {
					success = false
					return err
				}
			default:
				if err = buf.WriteByte(TagArgument); err != nil {
					return err
				}
				if err = reader.ReadRawTo(buf); err != nil {
					success = false
					return err
				}
			}
		case TagError:
			switch resultMode {
			case Normal, Serialized:
				reader.Reset()
				var e string
				if e, err = reader.ReadString(); err == nil {
					err = errors.New(e)
					if e := reader.CheckTag(TagEnd); e != nil {
						success = false
						err = e
					}
				} else {
					success = false
				}
				return err
			default:
				if err = buf.WriteByte(TagError); err != nil {
					return err
				}
				if err = reader.ReadRawTo(buf); err != nil {
					success = false
					return err
				}
			}
		}
	}
	if err != nil {
		success = false
		return err
	}
	switch resultMode {
	case RawWithEndTag:
		if err = buf.WriteByte(TagEnd); err != nil {
			return err
		}
		fallthrough
	case Raw:
		if err = setResult(result[0], buf); err != nil {
			return err
		}
	}
	return err
}

func (client *BaseClient) createRemoteObject(ro interface{}) {
	v := reflect.ValueOf(ro).Elem()
	t := v.Type()
	et := t
	if et.Kind() == reflect.Ptr {
		et = et.Elem()
	}
	objPointer := reflect.New(et)
	obj := objPointer.Elem()
	count := obj.NumField()
	for i := 0; i < count; i++ {
		f := obj.Field(i)
		if f.Kind() == reflect.Func {
			f.Set(reflect.MakeFunc(f.Type(), client.remoteMethod(f.Type(), et.Field(i))))
		}
	}
	if t.Kind() == reflect.Ptr {
		v.Set(objPointer)
	} else {
		v.Set(obj)
	}
}

func (client *BaseClient) remoteMethod(t reflect.Type, sf reflect.StructField) func(in []reflect.Value) []reflect.Value {
	name := getFuncName(sf)
	options := &InvokeOptions{ByRef: getByRef(sf), SimpleMode: getSimpleMode(sf), ResultMode: getResultMode(sf)}
	return func(in []reflect.Value) []reflect.Value {
		inlen := len(in)
		varlen := 0
		argc := inlen
		if t.IsVariadic() {
			argc--
			varlen = in[argc].Len()
			argc += varlen
		}
		args := make([]reflect.Value, argc)
		if argc > 0 {
			for i := 0; i < inlen-1; i++ {
				args[i] = in[i]
			}
			if t.IsVariadic() {
				v := in[inlen-1]
				for i := 0; i < varlen; i++ {
					args[inlen-1+i] = v.Index(i)
				}
			} else {
				args[inlen-1] = in[inlen-1]
			}
		}
		numout := t.NumOut()
		out := make([]reflect.Value, numout)
		switch numout {
		case 0:
			var result interface{}
			if err := <-client.invoke(name, args, options, []reflect.Value{reflect.ValueOf(&result).Elem()}); err == nil {
				return out
			} else {
				panic(err.Error())
			}
		case 1:
			rt0 := t.Out(0)
			if rt0.Kind() == reflect.Chan {
				if rt0.Elem().Kind() == reflect.Interface && rt0.Elem().Name() == "error" {
					var result chan interface{}
					err := client.invoke(name, args, options, []reflect.Value{reflect.ValueOf(&result).Elem()})
					out[0] = reflect.ValueOf(&err).Elem()
					return out
				} else {
					out[0] = reflect.New(rt0).Elem()
					client.invoke(name, args, options, out)
					return out
				}
			} else {
				if rt0.Kind() == reflect.Interface && rt0.Name() == "error" {
					var result interface{}
					err := <-client.invoke(name, args, options, []reflect.Value{reflect.ValueOf(&result).Elem()})
					out[0] = reflect.ValueOf(&err).Elem()
					return out
				} else {
					out[0] = reflect.New(rt0).Elem()
					if err := <-client.invoke(name, args, options, out); err == nil {
						return out
					} else {
						panic(err.Error())
					}
				}
			}
		default:
			last := numout - 1
			rtlast := t.Out(last)
			for i := 0; i < last; i++ {
				out[i] = reflect.New(t.Out(i)).Elem()
			}
			if rtlast.Kind() == reflect.Chan &&
				rtlast.Elem().Kind() == reflect.Interface &&
				rtlast.Elem().Name() == "error" {
				err := client.invoke(name, args, options, out[:last])
				out[last] = reflect.ValueOf(&err).Elem()
				return out
			}
			if rtlast.Kind() == reflect.Interface &&
				rtlast.Name() == "error" {
				err := <-client.invoke(name, args, options, out[:last])
				out[last] = reflect.ValueOf(&err).Elem()
				return out
			}
			out[last] = reflect.New(t.Out(last)).Elem()
			if t.Out(0).Kind() == reflect.Chan {
				client.invoke(name, args, options, out)
				return out
			} else {
				if err := <-client.invoke(name, args, options, out); err == nil {
					return out
				} else {
					panic(err.Error())
				}
			}
		}
		return out
	}
}

// public functions

func RegisterClientFactory(scheme string, newClient func(string) Client) {
	clientFactories[strings.ToLower(scheme)] = newClient
}

// private functions

func isStructPointer(p interface{}) bool {
	v := reflect.ValueOf(p)
	if !v.IsValid() || v.IsNil() {
		return false
	}
	t := v.Type()
	return t.Kind() == reflect.Ptr && (t.Elem().Kind() == reflect.Struct ||
		(t.Elem().Kind() == reflect.Ptr && t.Elem().Elem().Kind() == reflect.Struct))
}

func checkRefArgs(args []reflect.Value) bool {
	count := len(args)
	for i := 0; i < count; i++ {
		if args[i].Kind() != reflect.Ptr {
			return false
		}
	}
	return true
}

func setResult(result reflect.Value, buf *bytes.Buffer) error {
	switch result.Interface().(type) {
	case *bytes.Buffer:
		result.Set(reflect.ValueOf(buf))
	case []byte, interface{}:
		result.Set(reflect.ValueOf(buf.Bytes()))
	default:
		return errors.New("The argument result must be a *[]byte or **bytes.Buffer if the ResultMode is different from Normal.")
	}
	return nil
}

func getFuncName(sf reflect.StructField) string {
	keys := []string{"name", "Name", "funcname", "funcName", "FuncName"}
	for _, key := range keys {
		if name := sf.Tag.Get(key); name != "" {
			return name
		}
	}
	return sf.Name
}

func getByRef(sf reflect.StructField) interface{} {
	keys := []string{"byref", "byRef", "Byref", "ByRef"}
	for _, key := range keys {
		switch strings.ToLower(sf.Tag.Get(key)) {
		case "true", "t", "1":
			return true
		case "false", "f", "0":
			return false
		}
	}
	return nil
}

func getSimpleMode(sf reflect.StructField) interface{} {
	keys := []string{"simple", "Simple", "simpleMode", "SimpleMode"}
	for _, key := range keys {
		switch strings.ToLower(sf.Tag.Get(key)) {
		case "true", "t", "1":
			return true
		case "false", "f", "0":
			return false
		}
	}
	return nil
}

func getResultMode(sf reflect.StructField) ResultMode {
	keys := []string{"result", "Result", "resultMode", "ResultMode"}
	for _, key := range keys {
		switch strings.ToLower(sf.Tag.Get(key)) {
		case "normal":
			return Normal
		case "serialized":
			return Serialized
		case "raw":
			return Raw
		case "rawwithendtag":
			return RawWithEndTag
		}
	}
	return Normal
}

func init() {
	RegisterClientFactory("http", NewHttpClient)
	RegisterClientFactory("https", NewHttpClient)
	RegisterClientFactory("tcp", NewTcpClient)
	RegisterClientFactory("tcp4", NewTcpClient)
	RegisterClientFactory("tcp6", NewTcpClient)
	//RegisterClientFactory("ws", NewWebSocketClient)
}
