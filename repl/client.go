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
	// commands
	CommandRequireRepl    = `(require '[clojure.repl :refer :all])`
	CommandSetPrintLength = `(set! *print-length* 20)`
	CommandPublics        = `(clojure.string/join ", " (map first (ns-publics (ns-name *ns*))))`
	CommandReset          = `(map #(ns-unmap *ns* %) (keys (ns-interns *ns*)))`
	CommandShutdown       = `(System/exit 0)`
)

// Response is a response from PREPL
type Response struct {
	Tag          edn.Keyword `edn:"tag"`
	Value        string      `edn:"val,omitempty"`
	Namespace    string      `edn:"ns"`
	Milliseconds int64       `edn:"ms"`
	Form         string      `edn:"form"`
	Exception    bool        `edn:"exception,omitempty"`
	Message      string      `edn:"message,omitempty"`
}

// ExceptionValue struct for exception :value of Response
type ExceptionValue struct {
	Cause string      `edn:"cause"`
	Phase edn.Keyword `edn:"phase"`
}

// Client is a PREPL client
type Client struct {
	clojureBinPath string
	host           string
	port           int

	conn net.Conn
	sync.Mutex

	Verbose bool
}

// NewClient returns a new client
func NewClient(clojureBinPath, host string, port int) *Client {
	addr := fmt.Sprintf("%s:%d", host, port)

	client := Client{
		clojureBinPath: clojureBinPath,
		host:           host,
		port:           port,
		conn:           nil,
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
			log.Printf("failed to connect to existing PREPL connection, trying to launch: %s", client.clojureBinPath)

			// start a new PREPL server
			replCmd := exec.Command(
				client.clojureBinPath,
				fmt.Sprintf(`-J-Dclojure.server.jvm={:address "%s" :port %d :accept clojure.core.server/io-prepl}`, client.host, client.port),
			)
			go func(cmd *exec.Cmd) {
				cmd.Stdin = os.Stdin
				if err := cmd.Run(); err != nil {
					if cmd.Stderr != nil {
						panic(cmd.Stderr)
					}

					panic(err)
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

					client.initialize()

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

// initialize this client
func (c *Client) initialize() {
	for _, cmd := range []string{
		CommandRequireRepl,
		CommandSetPrintLength,
		// TODO - add more initialization codes here
	} {
		if _, err := c.Eval(cmd); err != nil {
			log.Printf("failed to evaluate `%s`: %s", cmd, err)
		}
	}
}

// Eval evaluates given code
func (c *Client) Eval(code string) (responses []Response, err error) {
	c.Lock()

	if c.Verbose {
		log.Printf("will evaluate `%s`", code)
	}

	responses, err = c.sendAndRecv(code)

	if c.Verbose {
		log.Printf("evaluated `%s`: %+v", code, responses)
	}

	c.Unlock()

	return responses, err
}

// LoadFile loads given file
func (c *Client) LoadFile(filepath string) (responses []Response, err error) {
	c.Lock()

	if c.Verbose {
		log.Printf("will load file `%s`", filepath)
	}

	responses, err = c.sendAndRecv(fmt.Sprintf(`(load-file "%s")`, filepath))

	if c.Verbose {
		log.Printf("loaded file `%s`: %+v", filepath, responses)
	}

	c.Unlock()

	return responses, err
}

// Shutdown shuts down the REPL, it will be the best place for cleaning things up
func (c *Client) Shutdown() {
	c.Lock()

	log.Printf("sending shutdown command to REPL...")

	if _, err := c.sendAndRecv(CommandShutdown); err != nil {
		log.Printf("failed to send shutdown command to REPL: %s", err)
	}

	log.Printf("closing connection to REPL...")

	if err := c.conn.Close(); err != nil {
		log.Printf("failed to close connection to REPL: %s", err)
	}

	c.Unlock()
}

// send request and receive response bytes from PREPL
func (c *Client) sendAndRecvBytes(request string) (result []byte, err error) {
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

	if c.Verbose {
		log.Printf("read buffer: %+v", buffer)
	}

	// only when read buffer is filled up,
	if buffer.Len() > 0 {
		return cleanse(buffer.Bytes()), nil
	}

	return []byte{}, err
}

// send request and receive response from PREPL
func (c *Client) sendAndRecv(request string) (responses []Response, err error) {
	responses = []Response{}

	var bts []byte
	if bts, err = c.sendAndRecvBytes(request); err == nil {
		var r Response
		for _, line := range bytes.Split(bts, []byte("\n")) {
			// skip empty lines
			if len(strings.TrimSpace(string(line))) <= 0 {
				continue
			}

			if err = edn.Unmarshal(line, &r); err == nil {
				responses = append(responses, r)
			} else {
				log.Printf("failed to unmarshal received response: %+v (%s)", r, err)
			}
		}
	}

	return responses, err
}

// RespToString converts REPL response to string
func RespToString(responses []Response) string {
	msgs := []string{}

	for _, r := range responses {
		if r.Exception { // PREPL error exists
			var exception ExceptionValue
			if err := edn.Unmarshal([]byte(r.Value), &exception); err == nil {
				msgs = append(msgs, strings.TrimSpace(exception.Cause))
			} else {
				errStr := fmt.Sprintf("failed to unmarshal exception value: %s", err)

				log.Print(errStr)

				switch r.Tag {
				case "ret":
					msgs = append(msgs, fmt.Sprintf("%s=> %s", r.Namespace, strings.TrimSpace(r.Value)))
				case "out", "err":
					msgs = append(msgs, strings.TrimSpace(r.Value))
				default:
					msgs = append(msgs, errStr)
				}
			}
		} else {
			switch r.Tag {
			case "ret":
				msgs = append(msgs, fmt.Sprintf("%s=> %s", r.Namespace, strings.TrimSpace(r.Value)))
			case "out", "err":
				msgs = append(msgs, strings.TrimSpace(r.Value))
			default:
				errStr := fmt.Sprintf("unhandled `%s` response: %+v", r.Tag, r)

				log.Print(errStr)

				msgs = append(msgs, errStr)
			}
		}
	}

	// join them
	return strings.Join(msgs, "\n")
}

// following strings lead to go-edn's parser errors, so need to be replaced...
var invalidStrings = []string{
	"#:clojure.error",
	"#:clojure.spec.alpha",
	"#object",
}

// regular expression for hex numbers
var reHex = regexp.MustCompile(`(0x[0-9a-fA-F]+)`)

// cleanse string (edn parser fails on some characters...)
func cleanse(original []byte) (result []byte) {
	result = original

	// XXX - remove invalid strings
	for _, str := range invalidStrings {
		result = bytes.ReplaceAll(result, []byte(str), []byte(""))
	}

	// XXX - go-edn fails to parse hex numbers, so replace them to strings
	result = []byte(reHex.ReplaceAllString(string(result), `\"$1\"`))

	return result
}
