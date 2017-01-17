package main

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
	ReplConnectTimeoutSeconds = 10
	ReplBootupTimeoutSeconds  = 60
	ProcessTimeoutSeconds     = 3

	NumBytes            = 1024
	NumRetries          = 128
	TimeoutMilliseconds = 100
)

type cmd struct {
	op   string
	code string
}

const (
	OpEval = "eval"

	ReplCommandReset    = `(map #(ns-unmap *ns* %) (keys (ns-interns *ns*)))`
	ReplCommandShutdown = `(System/exit 0)`
)

// response from nREPL
type resp struct {
	Ns      string        `name:"ns"`      // namespace
	Out     string        `name:"out"`     // stdout
	Session string        `name:"session"` // session
	Value   string        `name:"value"`   // value
	Ex      string        `name:"ex"`      // exception
	RootEx  string        `name:"root-ex"` // root exception
	Op      string        `name:"op"`      // operation
	Status  []interface{} `name:"status"`  // status (on nREPL errors)
}

// if there were any nREPL error, it will be placed in 'Status'
func (r *resp) HasError() bool {
	return len(r.Status) > 0
}

// for struct-interface key mapping
var respKeyMaps map[string]string

func init() {
	respKeyMaps = make(map[string]string)

	// read 'name' tags
	// https://sosedoff.com/2016/07/16/golang-struct-tags.html
	t := reflect.TypeOf(resp{})
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

// nREPL client
type ReplClient struct {
	LeinPath string
	Host     string
	Port     int

	conn net.Conn

	sync.Mutex
}

// get a new client
func NewClient(leinPath, host string, port int) *ReplClient {
	addr := fmt.Sprintf("%s:%d", host, port)

	client := ReplClient{
		LeinPath: leinPath,
		Host:     host,
		Port:     port,
		conn:     nil,
	}

	// wait for nREPL
	for i := 0; i < ReplConnectTimeoutSeconds; i++ {
		time.Sleep(1 * time.Second)
		if conn, err := net.Dial("tcp", addr); err == nil {
			client.conn = conn

			log.Printf("There is an existing nREPL on: %s\n", addr)
			break
		}

		if i == (ReplConnectTimeoutSeconds - 1) {
			log.Printf("Failed to connect to nREPL, trying to launch: %s\n", leinPath)

			// start nREPL server (ex: $ lein repl :headless :port 9999)
			replCmd := exec.Command(leinPath, "repl", ":headless", ":port", strconv.Itoa(port))
			go func(cmd *exec.Cmd) {
				if err := cmd.Run(); err != nil {
					panic(err)
				}
			}(replCmd)

			log.Printf("Waiting for nREPL to bootup...\n")

			// wait for nREPL
			for i := 0; i < ReplBootupTimeoutSeconds; i++ {
				time.Sleep(1 * time.Second)
				if conn, err := net.Dial("tcp", addr); err == nil {
					client.conn = conn

					log.Printf("Connected to nREPL on: %s\n", addr)
					break
				}

				if i == (ReplBootupTimeoutSeconds - 1) {
					panic("Failed to connect to launched nREPL: " + addr)
				}
			}
		}
	}

	return &client
}

// evaluate given code
func (c *ReplClient) Eval(code string) (received resp, err error) {
	c.Lock()
	received, err = c.sendAndRecv(cmd{op: OpEval, code: code})
	c.Unlock()

	return received, err
}

// load given file
func (c *ReplClient) LoadFile(filepath string) (received resp, err error) {
	c.Lock()
	received, err = c.sendAndRecv(cmd{op: OpEval, code: fmt.Sprintf(`(load-file "%s")`, filepath)})
	c.Unlock()

	return received, err
}

// best place for cleaning things up
func (c *ReplClient) Shutdown() {
	c.Lock()

	var err error

	// shutdown nREPL
	log.Printf("Sending shutdown command to REPL...\n")
	_, err = c.sendAndRecv(cmd{op: OpEval, code: ReplCommandShutdown})
	if err != nil {
		log.Printf("Failed to sending shutdown command to REPL: %s\n", err)
	}

	// close connection to nREPL
	log.Printf("Closing connection to REPL...\n")
	err = c.conn.Close()
	if err != nil {
		log.Printf("Failed to close connection to REPL: %s\n", err)
	}

	c.Unlock()
}

// send request and receive response from nREPL
func (c *ReplClient) sendAndRecv(request interface{}) (received resp, err error) {
	buffer := bytes.NewBuffer([]byte{})

	// set read timeout
	if err = c.conn.SetReadDeadline(time.Now().Add(TimeoutMilliseconds * time.Millisecond)); err != nil {
		log.Printf("Error while setting read deadline: %s\n", err)

		return resp{}, err
	}

	// send BEncoded request
	if err = bencode.Marshal(c.conn, request); err == nil {
		numRead := 0
		buf := make([]byte, NumBytes)

		for n := 0; n < NumRetries; n++ {
			if numRead, err = c.conn.Read(buf); err == nil {
				if numRead > 0 {
					buffer.Write(buf[:numRead])
				}
			} else {
				if err != io.EOF && !(err.(net.Error)).Timeout() {
					log.Printf("Error while reading bytes: %s\n", err)
					break
				}
			}
		}
	} else {
		log.Printf("Error while writing request: %s\n", err)
	}

	// only when read buffer is filled up,
	if buffer.Len() > 0 {
		var decoded interface{}
		if decoded, err = bencode.Decode(buffer); err == nil {
			switch decoded.(type) {
			case map[string]interface{}:
				response := resp{}
				if err = fillRespStruct(&response, decoded.(map[string]interface{})); err == nil {
					return response, nil
				} else {
					log.Printf("Failed to fill struct: %s\n", err)
				}
			default:
				log.Printf("Received non-expected type: %T\n", decoded)
			}
		} else {
			log.Printf("Error while decoding BEncode: %s (%s)\n", err, buffer.String())
		}
	}

	return resp{}, err
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
		return fmt.Errorf("No such field: '%s'", name)
	}
	if !structFieldValue.CanSet() {
		return fmt.Errorf("Cannot set '%s' field value", name)
	}

	structFieldType := structFieldValue.Type()
	val := reflect.ValueOf(value)

	if structFieldType != val.Type() {
		return fmt.Errorf("Provided value type doesn't match: %+v and %+v", structFieldType, val.Type())
	}

	structFieldValue.Set(val)

	return nil
}
