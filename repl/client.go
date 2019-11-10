package repl

// PREPL client codes

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"olympos.io/encoding/edn"
)

// constants
const (
	replConnectTimeoutSeconds = 10
	replBootupTimeoutSeconds  = 60

	numBytes            = 10 * 1024 // 10 kb
	numRetries          = 10        // retry upto 10 times
	timeoutMilliseconds = 1000      // 1 second
)

// Operations and commands
const (
	// operations
	OpEval = "eval"

	// commands
	CommandRequireRepl    = `(require '[clojure.repl :refer :all])`
	CommandSetPrintLength = `(set! *print-length* 20)`
	CommandReset          = `(map #(ns-unmap *ns* %) (keys (ns-interns *ns*)))`
	CommandShutdown       = `(System/exit 0)`
)

// Resp is a response from PREPL
type Resp struct {
	Tag         edn.Keyword `edn:"tag"`
	Value       string      `edn:"val,omitempty"`
	Namespace   string      `edn:"ns"`
	Millisecond int64       `edn:"ms"`
	Form        string      `edn:"form"`
	Exception   bool        `edn:"exception,omitempty"`
	Message     string      `edn:"message,omitempty"`
}

// ExceptionValue struct for exception :value of Resp
type ExceptionValue struct {
	Cause string      `edn:"cause"`
	Phase edn.Keyword `edn:"phase"`
}

// Client is a PREPL client
type Client struct {
	CljPath string
	Host    string
	Port    int

	Verbose bool

	conn net.Conn

	sync.Mutex
}

// NewClient returns a new client
func NewClient(cljPath, host string, port int) *Client {
	addr := fmt.Sprintf("%s:%d", host, port)

	client := Client{
		CljPath: cljPath,
		Host:    host,
		Port:    port,
		conn:    nil,
	}

	// wait for PREPL
	for i := 0; i < replConnectTimeoutSeconds; i++ {
		time.Sleep(1 * time.Second)
		if conn, err := net.Dial("tcp", addr); err == nil {
			client.conn = conn

			log.Printf("there is an existing PREPL on: %s", addr)
			break
		}

		if i == (replConnectTimeoutSeconds - 1) {
			log.Printf("failed to connect to existing PREPL connection, trying to launch: %s", cljPath)

			// start a new PREPL server
			replCmd := exec.Command(
				cljPath,
				fmt.Sprintf(`-J-Dclojure.server.jvm={:address "%s" :port %d :accept clojure.core.server/io-prepl}`, host, port),
			)
			go func(cmd *exec.Cmd) {
				cmd.Stdin = os.Stdin
				if err := cmd.Run(); err != nil {
					panic(cmd.Stderr)
				}

				log.Printf("PREPL exited...")
			}(replCmd)

			log.Printf("waiting for PREPL to bootup...")

			// wait for PREPL
			for i := 0; i < replBootupTimeoutSeconds; i++ {
				log.Printf("connecting to PREPL on: %s", addr)

				time.Sleep(1 * time.Second)
				if conn, err := net.Dial("tcp", addr); err == nil {
					client.conn = conn

					log.Printf("connected to PREPL on: %s", addr)

					client.Initialize()

					break
				}

				if i == (replBootupTimeoutSeconds - 1) {
					panic("failed to connect to launched PREPL: " + addr)
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
	if _, err := c.Eval(CommandSetPrintLength); err != nil {
		log.Printf("failed to set `*print-length*`")
	}

	// TODO - add more initialization codes here
}

// Eval evaluates given code
func (c *Client) Eval(code string) (received []Resp, err error) {
	c.Lock()

	if c.Verbose {
		log.Printf("evaluating: %s", code)
	}

	received, err = c.sendAndRecv(code)

	if c.Verbose {
		log.Printf("evaluated: %+v", received)
	}

	c.Unlock()

	return received, err
}

// LoadFile loads given file
func (c *Client) LoadFile(filepath string) (received []Resp, err error) {
	c.Lock()
	received, err = c.sendAndRecv(fmt.Sprintf(`(load-file "%s")`, filepath))
	c.Unlock()

	return received, err
}

// Shutdown shuts down the REPL, it will be the best place for cleaning things up
func (c *Client) Shutdown() {
	c.Lock()

	var err error

	// shutdown PREPL
	log.Printf("sending shutdown command to REPL...")
	_, err = c.sendAndRecv(CommandShutdown)
	if err != nil {
		log.Printf("failed to send shutdown command to REPL: %s", err)
	}

	// close connection to PREPL
	log.Printf("closing connection to REPL...")
	err = c.conn.Close()
	if err != nil {
		log.Printf("failed to close connection to REPL: %s", err)
	}

	c.Unlock()
}

// send request and receive response bytes from PREPL
func (c *Client) sendAndRecvBytes(request string) (bts []byte, err error) {
	buffer := bytes.NewBuffer([]byte{})

	// set read timeout
	if err = c.conn.SetReadDeadline(time.Now().Add(timeoutMilliseconds * time.Millisecond)); err != nil {
		log.Printf("error while setting read deadline: %s", err)

		return []byte{}, err
	}

	if c.Verbose {
		log.Printf("writing request: %s", request)
	}

	// send request (with trailing newline)
	if _, err = c.conn.Write([]byte(request + "\n")); err == nil {
		// read response
		buf := make([]byte, numBytes)
		for n := 0; n < numRetries; n++ {
			if numRead, readErr := c.conn.Read(buf); readErr == nil {
				if numRead > 0 {
					buffer.Write(buf[:numRead])
				}
			} else {
				if readErr != io.EOF && !(readErr.(net.Error)).Timeout() {
					log.Printf("error while reading bytes: %s", readErr)
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
		return cleanse(buffer.Bytes()), nil
	}

	if err == nil {
		return []byte{}, fmt.Errorf("buffer is not filled up")
	}

	return []byte{}, err
}

// send request and receive response from PREPL
func (c *Client) sendAndRecv(request string) (received []Resp, err error) {
	received = []Resp{}

	var bts []byte
	if bts, err = c.sendAndRecvBytes(request); err == nil {
		var r Resp
		for _, line := range bytes.Split(bts, []byte("\n")) {
			// skip empty lines
			if len(strings.TrimSpace(string(line))) <= 0 {
				continue
			}

			if err = edn.Unmarshal(line, &r); err == nil {
				received = append(received, r)
			} else {
				log.Printf("failed to unmarshal received response: %+v (%s)", r, err)
			}
		}
	}

	return received, err
}

// RespToString converts REPL response to string
func RespToString(received []Resp) string {
	msgs := []string{}

	for _, r := range received {
		if r.Exception { // PREPL error
			var exception ExceptionValue
			if err := edn.Unmarshal([]byte(r.Value), &exception); err == nil {
				msgs = append(msgs, exception.Cause)
			} else {
				errStr := fmt.Sprintf("failed to unmarshal exception value: %s", err)

				log.Printf(errStr)

				msgs = append(msgs, errStr)
			}
		} else {
			switch r.Tag {
			case "ret":
				// namespace,
				msgs = append(msgs, fmt.Sprintf("%s=> %s", r.Namespace, r.Value))
			case "out":
				msgs = append(msgs, fmt.Sprintf("%s", r.Value))
			default:
				errStr := fmt.Sprintf("unmatched response tag: %s", r.Tag)

				log.Printf(errStr)

				msgs = append(msgs, errStr)
			}
		}
	}

	// join them
	return strings.Join(msgs, "") // already has newline
}

// following strings lead to edn parser errors
var invalidStrings = []string{
	"#:clojure.error",
	"#:clojure.spec.alpha",
	"#object",
}

// cleanse string (edn parser fails on some characters...)
func cleanse(original []byte) (result []byte) {
	result = original

	// XXX - remove invalid strings
	// => invalid character ':' after token starting with "#"
	for _, str := range invalidStrings {
		result = bytes.ReplaceAll(result, []byte(str), []byte(""))
	}

	// XXX - replace hex numbers to strings
	// => go-edn fails to parse hex numbers...
	reHex := regexp.MustCompile(`(0x[0-9a-fA-F]+)`)
	result = []byte(reHex.ReplaceAllString(string(result), `\"$1\"`))

	return result
}
