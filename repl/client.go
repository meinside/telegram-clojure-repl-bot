package repl

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
	"strings"
	"sync"
	"time"

	bencode "github.com/jackpal/bencode-go"
)

// constants
const (
	replConnectTimeoutSeconds = 10
	replBootupTimeoutSeconds  = 60

	numBytes            = 10 * 1024 // 10 kb
	numRetries          = 10        // retry upto 10 times
	timeoutMilliseconds = 1000      // 1 second

	replProfileName = "headless-repl"
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
	CommandRequireRepl = `(require '[clojure.repl :refer :all])`
	CommandReset       = `(map #(ns-unmap *ns* %) (keys (ns-interns *ns*)))`
	CommandShutdown    = `(System/exit 0)`
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

// Client is a nREPL client
type Client struct {
	LeinPath string
	Host     string
	Port     int

	Verbose bool

	conn net.Conn

	sync.Mutex
}

// NewClient returns a new client
func NewClient(leinPath, host string, port int) *Client {
	addr := fmt.Sprintf("%s:%d", host, port)

	client := Client{
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

			log.Printf("there is an existing nREPL on: %s", addr)
			break
		}

		if i == (replConnectTimeoutSeconds - 1) {
			log.Printf("failed to connect to nREPL, trying to launch: %s", leinPath)

			// start nREPL server
			replCmd := exec.Command(leinPath, "with-profile", replProfileName, "repl", ":headless", ":port", strconv.Itoa(port))
			go func(cmd *exec.Cmd) {
				if err := cmd.Run(); err != nil {
					panic(err)
				}
			}(replCmd)

			log.Printf("waiting for nREPL to bootup...")

			// wait for nREPL
			for i := 0; i < replBootupTimeoutSeconds; i++ {
				time.Sleep(1 * time.Second)
				if conn, err := net.Dial("tcp", addr); err == nil {
					client.conn = conn

					log.Printf("connected to nREPL on: %s", addr)

					client.Initialize()

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

// Initialize initializes this client
func (c *Client) Initialize() {
	if _, err := c.Eval(CommandRequireRepl); err != nil {
		log.Printf("failed to require `clojure.repl`")
	}

	// TODO - add more initialization codes here
}

// Eval evaluates given code
func (c *Client) Eval(code string) (received Resp, err error) {
	c.Lock()

	if c.Verbose {
		log.Printf("evaluating: %s", code)
	}

	received, err = c.sendAndRecv(cmd{op: OpEval, code: code})

	if c.Verbose {
		log.Printf("evaluated: %s", received)
	}

	c.Unlock()

	return received, err
}

// LoadFile loads given file
func (c *Client) LoadFile(filepath string) (received Resp, err error) {
	c.Lock()
	received, err = c.sendAndRecv(cmd{op: OpEval, code: fmt.Sprintf(`(load-file "%s")`, filepath)})
	c.Unlock()

	return received, err
}

// Shutdown shuts down the REPL, it will be the best place for cleaning things up
func (c *Client) Shutdown() {
	c.Lock()

	var err error

	// shutdown nREPL
	log.Printf("sending shutdown command to REPL...")
	_, err = c.sendAndRecv(cmd{op: OpEval, code: CommandShutdown})
	if err != nil {
		log.Printf("failed to send shutdown command to REPL: %s", err)
	}

	// close connection to nREPL
	log.Printf("closing connection to REPL...")
	err = c.conn.Close()
	if err != nil {
		log.Printf("failed to close connection to REPL: %s", err)
	}

	c.Unlock()
}

// send request and receive response from nREPL
func (c *Client) sendAndRecv(request interface{}) (received Resp, err error) {
	buffer := bytes.NewBuffer([]byte{})

	// set read timeout
	if err = c.conn.SetReadDeadline(time.Now().Add(timeoutMilliseconds * time.Millisecond)); err != nil {
		log.Printf("error while setting read deadline: %s", err)

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
					log.Printf("error while reading bytes: %s", err)
					break
				}
			}
		}
	} else {
		log.Printf("error while writing request: %s", err)
	}

	// log for debugging
	if c.Verbose {
		log.Printf(">>> read buffer: %+v", buffer)
	}

	// only when read buffer is filled up,
	if buffer.Len() > 0 {
		var decoded interface{}
		if decoded, err = bencode.Decode(buffer); err == nil {
			switch decoded.(type) {
			case string:
				return Resp{Value: decoded.(string)}, nil
			case int64:
				return Resp{Value: fmt.Sprintf("%d", decoded.(int64))}, nil
			case uint64:
				return Resp{Value: fmt.Sprintf("%d", decoded.(uint64))}, nil
			case []interface{}:
				return Resp{Value: fmt.Sprintf("%v", decoded)}, nil
			case map[string]interface{}:
				response := Resp{}
				if err = fillRespStruct(&response, decoded.(map[string]interface{})); err == nil {
					return response, nil
				}

				log.Printf("failed to fill struct: %s", err)
			default:
				log.Printf("received non-expected type: %T", decoded)
			}
		} else {
			log.Printf("error while decoding BEncode: %s (%s)", err, buffer.String())
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

// RespToString converts REPL response to string
func RespToString(received Resp) string {
	msgs := []string{}

	if received.HasError() { // nREPL error
		// join status strings
		for _, s := range received.Status {
			msgs = append(msgs, fmt.Sprintf("%v", s))
		}
		status := strings.Join(msgs, ", ")

		// show statuses and exceptions
		if received.Ex == received.RootEx {
			return fmt.Sprintf("%s: %s\n", status, received.Ex)
		}

		return fmt.Sprintf("%s: %s (%s)\n", status, received.Ex, received.RootEx)
	}

	// no error

	// if response has namespace,
	if len(received.Ns) > 0 {
		msgs = append(msgs, fmt.Sprintf("%s=> %s", received.Ns, received.Value))
	}

	// if response has a string from stdout,
	if len(received.Out) > 0 {
		msgs = append(msgs, fmt.Sprintf("%s", received.Out))
	}

	// if response has an error,
	if len(received.Err) > 0 {
		msgs = append(msgs, fmt.Sprintf("%s", received.Err))
	}

	// join them
	return strings.Join(msgs, "\n")
}
