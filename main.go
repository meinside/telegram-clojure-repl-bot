package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	telegram "github.com/meinside/telegram-bot-go"
)

const (
	ConfigFilename = "config.json"

	TempDir = "/tmp"
)

const (
	DefaultMonitorInterval = 3

	// telegram commands
	CommandStart = "/start"
	CommandReset = "/reset"

	// telegram messages
	MessageWelcome       = "Welcome!"
	MessageFailedToReset = "Failed to reset REPL."
)

type config struct {
	ApiToken        string   `json:"api_token"`
	LeinExecPath    string   `json:"lein_exec_path"`
	ReplHost        string   `json:"repl_host"`
	ReplPort        int      `json:"repl_port"`
	AllowedIds      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`
	IsVerbose       bool     `json:"is_verbose,omitempty"`
}

var _apiToken string
var _leinExecPath string
var _replHost string
var _replPort int
var _monitorInterval int
var _allowedIds []string
var _isVerbose bool

// read config file
func openConfig() (config, error) {
	_, filename, _, _ := runtime.Caller(0) // = __FILE__

	if file, err := ioutil.ReadFile(filepath.Join(path.Dir(filename), ConfigFilename)); err == nil {
		var conf config
		if err := json.Unmarshal(file, &conf); err == nil {
			return conf, nil
		} else {
			return config{}, err
		}
	} else {
		return config{}, err
	}
}

func init() {
	// read config
	if conf, err := openConfig(); err != nil {
		panic(err)
	} else {
		_apiToken = conf.ApiToken
		_leinExecPath = conf.LeinExecPath
		_replHost = conf.ReplHost
		_replPort = conf.ReplPort

		if conf.MonitorInterval <= 0 {
			conf.MonitorInterval = DefaultMonitorInterval
		}
		_monitorInterval = conf.MonitorInterval
		_allowedIds = conf.AllowedIds
		_isVerbose = conf.IsVerbose
	}
}

// check if given Telegram id is allowed or not
func isAllowedId(id string) bool {
	for _, v := range _allowedIds {
		if v == id {
			return true
		}
	}
	return false
}

func main() {
	client := NewClient(_leinExecPath, _replHost, _replPort)

	// catch SIGINT and SIGTERM and terminate gracefully
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func(client *ReplClient) {
		<-sig
		client.Shutdown() // shutdown client
		os.Exit(1)
	}(client)

	bot := telegram.NewClient(_apiToken)
	bot.Verbose = _isVerbose

	// get info about this bot
	if me := bot.GetMe(); me.Ok {
		log.Printf("Starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := bot.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			bot.StartMonitoringUpdates(0, _monitorInterval, func(b *telegram.Bot, update telegram.Update, err error) {
				if err == nil {
					handleUpdate(b, update, client)
				} else {
					log.Printf("*** Error while receiving update (%s)", err.Error())
				}
			})
		} else {
			panic("Failed to delete webhook")
		}
	} else {
		panic("Failed to get info of the bot")
	}
}

// handle received update from Telegram server
func handleUpdate(b *telegram.Bot, update telegram.Update, client *ReplClient) {
	if update.HasMessage() || update.HasEditedMessage() {
		var message *telegram.Message
		if update.HasMessage() {
			message = update.Message
		} else { // if update.HasEditedMessage() {
			message = update.EditedMessage
		}

		var str string
		username := *message.From.Username
		if !isAllowedId(username) { // check if this user is allowed to use this bot
			log.Printf("*** Received an update from an unauthorized user: @%s", username)

			str = fmt.Sprintf("Your id: @%s is not allowed to use this bot.", username)
		} else {
			// 'is typing...'
			b.SendChatAction(message.Chat.Id, telegram.ChatActionTyping)

			if message.HasText() {
				switch *message.Text {
				case CommandStart:
					str = MessageWelcome
				case CommandReset:
					if received, err := client.Eval(ReplCommandReset); err == nil {
						str = fmt.Sprintf("%s=> %s", received.Ns, received.Value)
					} else {
						str = MessageFailedToReset
					}
				default:
					if received, err := client.Eval(*message.Text); err == nil {
						str = stringFromResponse(received)
					} else {
						str = fmt.Sprintf("Error: %s", err)
					}
				}
			} else if message.HasDocument() {
				fileResult := b.GetFile(message.Document.FileId)
				fileUrl := b.GetFileUrl(*fileResult.Result)

				// download the file (as temporary)
				if filepath, err := downloadTemporarily(fileUrl); err == nil {
					if received, err := client.LoadFile(filepath); err == nil {
						str = stringFromResponse(received)

						// and delete it
						if err := os.Remove(filepath); err != nil {
							log.Printf("*** Failed to delete file %s: %s", filepath, err)
						}
					} else {
						str = fmt.Sprintf("Failed load file: %s", err)
					}
				} else {
					str = fmt.Sprintf("Failed to download the document: %s", err)
				}
			} else {
				str = fmt.Sprintf("Error: couldn't process your message.")
			}
		}

		// send message
		if sent := b.SendMessage(message.Chat.Id, str, map[string]interface{}{
			"reply_markup": telegram.ReplyKeyboardMarkup{ // show keyboards
				Keyboard: [][]telegram.KeyboardButton{
					[]telegram.KeyboardButton{
						telegram.KeyboardButton{
							Text: CommandReset,
						},
					},
				},
				ResizeKeyboard: true,
			},
		}); !sent.Ok {
			log.Printf("*** Failed to send message: %s", *sent.Description)
		}
	} else {
		log.Printf("*** Received update has no message")
	}
}

// download given url
func downloadTemporarily(url string) (filepath string, err error) {
	tokens := strings.Split(url, "/")
	filename := tokens[len(tokens)-1] // get the last path segment

	filepath = path.Join(TempDir, filename)

	var f *os.File
	if f, err = os.Create(filepath); err == nil {
		defer f.Close()

		var response *http.Response
		if response, err = http.Get(url); err == nil {
			defer response.Body.Close()

			if _, err = io.Copy(f, response.Body); err == nil {
				return filepath, nil
			}
		}
	}

	return "", err
}

// get string from REPL response
func stringFromResponse(received resp) string {
	strs := []string{}
	if received.HasError() { // nREPL error
		// join status strings
		for _, s := range received.Status {
			strs = append(strs, fmt.Sprintf("%v", s))
		}
		status := strings.Join(strs, ", ")

		// show statuses and exceptions
		if received.Ex == received.RootEx {
			return fmt.Sprintf("%s: %s\n", status, received.Ex)
		} else {
			return fmt.Sprintf("%s: %s (%s)\n", status, received.Ex, received.RootEx)
		}
	} else { // no error
		// if response has namespace,
		if len(received.Ns) > 0 {
			strs = append(strs, fmt.Sprintf("%s=> %s", received.Ns, received.Value))
		}

		// if response has a string from stdout,
		if len(received.Out) > 0 {
			strs = append(strs, fmt.Sprintf("%s", received.Out))
		}

		// join them
		return strings.Join(strs, "\n")
	}
}
