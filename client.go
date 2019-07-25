package main

// nREPL client codes

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"reflect"
	"strconv"
	"sync"
	"time"

	bencode "github.com/jackpal/bencode-go"
)

const (
	replConnectTimeoutSeconds = 10
	replBootupTimeoutSeconds  = 60

	numBytes            = 1024
	numRetries          = 128
	timeoutMilliseconds = 100
)

type cmd struct {
	op   string
	code string
}

// Operations and commands
const (
	// operations
	OpEval = "eval"

	// commands
	ReplCommandReset    = `(map #(ns-unmap *ns* %) (keys (ns-interns *ns*)))`
	ReplCommandShutdown = `(System/exit 0)`
)

// Resp is a response from nREPL
type Resp struct {
	Ns      string        `name:"ns"`      // namespace
	Out     string        `name:"out"`     // stdout
	Session string        `name:"session"` // session
	Value   string        `name:"value"`   // value
	Ex      string        `name:"ex"`      // exception
	RootEx  string        `name:"root-ex"` // root exception
	Op      string        `name:"op"`      // operation
	Status  []interface{} `name:"status"`  // status (on nREPL errors)
	Err     string        `name:"err"`     // error
}

// HasError returns whether there was any nREPL error
func (r *Resp) HasError() bool {
	return len(r.Status) > 0
}

// for struct-interface key mapping
var respKeyMaps map[string]string

func init() {
	respKeyMaps = make(map[string]string)

	// read 'name' tags
	// https://sosedoff.com/2016/07/16/golang-struct-tags.html
	t := reflect.TypeOf(Resp{})
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("name")
		if len(tag) > 0 {
			respKeyMaps[tag] = field.Name
		} else {
			respKeyMaps[field.Name] = field.Name
		}
	}

}

// ReplClient is a nREPL client
type ReplClient struct {
	LeinPath string
	Host     string
	Port     int

	conn net.Conn

	sync.Mutex
}

// NewClient returns a new client
func NewClient(leinPath, host string, port int) *ReplClient {
	addr := fmt.Sprintf("%s:%d", host, port)

	client := ReplClient{
		LeinPath: leinPath,
		Host:     host,
		Port:     port,
		conn:     nil,
	}

	// wait for nREPL
	for i := 0; i < replConnectTimeoutSeconds; i++ {
		time.Sleep(1 * time.Second)
		if conn, err := net.Dial("tcp", addr); err == nil {
			client.conn = conn

			log.Printf("there is an existing nREPL on: %s\n", addr)
			break
		}

		if i == (replConnectTimeoutSeconds - 1) {
			log.Printf("failed to connect to nREPL, trying to launch: %s\n", leinPath)

			// start nREPL server (ex: $ lein repl :headless :port 9999)
			replCmd := exec.Command(leinPath, "repl", ":headless", ":port", strconv.Itoa(port))
			go func(cmd *exec.Cmd) {
				if err := cmd.Run(); err != nil {
					panic(err)
				}
			}(replCmd)

			log.Printf("waiting for nREPL to bootup...\n")

			// wait for nREPL
			for i := 0; i < replBootupTimeoutSeconds; i++ {
				time.Sleep(1 * time.Second)
				if conn, err := net.Dial("tcp", addr); err == nil {
					client.conn = conn

					log.Printf("connected to nREPL on: %s\n", addr)
					break
				}

				if i == (replBootupTimeoutSeconds - 1) {
					panic("failed to connect to launched nREPL: " + addr)
				}
			}
		}
	}

	return &client
}

// Eval evaluates given code
func (c *ReplClient) Eval(code string) (received Resp, err error) {
	c.Lock()
	received, err = c.sendAndRecv(cmd{op: OpEval, code: code})
	c.Unlock()

	return received, err
}

// LoadFile loads given file
func (c *ReplClient) LoadFile(filepath string) (received Resp, err error) {
	c.Lock()
	received, err = c.sendAndRecv(cmd{op: OpEval, code: fmt.Sprintf(`(load-file "%s")`, filepath)})
	c.Unlock()

	return received, err
}

// Shutdown shuts down the REPL, it will be the best place for cleaning things up
func (c *ReplClient) Shutdown() {
	c.Lock()

	var err error

	// shutdown nREPL
	log.Printf("sending shutdown command to REPL...\n")
	_, err = c.sendAndRecv(cmd{op: OpEval, code: ReplCommandShutdown})
	if err != nil {
		log.Printf("failed to sending shutdown command to REPL: %s\n", err)
	}

	// close connection to nREPL
	log.Printf("closing connection to REPL...\n")
	err = c.conn.Close()
	if err != nil {
		log.Printf("failed to close connection to REPL: %s\n", err)
	}

	c.Unlock()
}

// send request and receive response from nREPL
func (c *ReplClient) sendAndRecv(request interface{}) (received Resp, err error) {
	buffer := bytes.NewBuffer([]byte{})

	// set read timeout
	if err = c.conn.SetReadDeadline(time.Now().Add(timeoutMilliseconds * time.Millisecond)); err != nil {
		log.Printf("error while setting read deadline: %s\n", err)

		return Resp{}, err
	}

	// send BEncoded request
	if err = bencode.Marshal(c.conn, request); err == nil {
		numRead := 0
		buf := make([]byte, numBytes)

		for n := 0; n < numRetries; n++ {
			if numRead, err = c.conn.Read(buf); err == nil {
				if numRead > 0 {
					buffer.Write(buf[:numRead])
				}
			} else {
				if err != io.EOF && !(err.(net.Error)).Timeout() {
					log.Printf("error while reading bytes: %s\n", err)
					break
				}
			}
		}
	} else {
		log.Printf("error while writing request: %s\n", err)
	}

	// only when read buffer is filled up,
	if buffer.Len() > 0 {
		var decoded interface{}
		if decoded, err = bencode.Decode(buffer); err == nil {
			switch decoded.(type) {
			case map[string]interface{}:
				response := Resp{}
				if err = fillRespStruct(&response, decoded.(map[string]interface{})); err == nil {
					return response, nil
				}

				log.Printf("failed to fill struct: %s\n", err)
			default:
				log.Printf("received non-expected type: %T\n", decoded)
			}
		} else {
			log.Printf("error while decoding BEncode: %s (%s)\n", err, buffer.String())
		}
	}

	return Resp{}, err
}

// fill fields of given struct
//
// https://play.golang.org/p/0weG38IUA9
func fillRespStruct(s interface{}, m map[string]interface{}) error {
	for k, v := range m {
		if key, ok := respKeyMaps[k]; ok {
			if err := _setRespField(s, key, v); err != nil {
				return err
			}
		}
	}

	return nil
}

// set value for field of interface
//
// https://play.golang.org/p/0weG38IUA9
func _setRespField(obj interface{}, name string, value interface{}) error {
	structValue := reflect.ValueOf(obj).Elem()
	structFieldValue := structValue.FieldByName(name)

	if !structFieldValue.IsValid() {
		return fmt.Errorf("no such field: '%s'", name)
	}
	if !structFieldValue.CanSet() {
		return fmt.Errorf("cannot set '%s' field value", name)
	}

	structFieldType := structFieldValue.Type()
	val := reflect.ValueOf(value)

	if structFieldType != val.Type() {
		return fmt.Errorf("provided value type doesn't match: %+v and %+v", structFieldType, val.Type())
	}

	structFieldValue.Set(val)

	return nil
}
