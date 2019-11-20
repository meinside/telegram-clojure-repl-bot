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
	"strings"
	"syscall"

	telegram "github.com/meinside/telegram-bot-go"
	repl "github.com/meinside/telegram-bot-repl/repl"
)

const (
	configFilename = "config.json"

	tempDir = "/tmp"
)

const (
	defaultMonitorInterval = 3

	// telegram commands
	commandStart   = "/start"
	commandPublics = "/publics"
	commandReset   = "/reset"

	// telegram messages
	messageWelcome              = "welcome!"
	messageFailedToListPublics  = "failed to list public definitions."
	messageFailedToReset        = "failed to reset REPL."
	messageErrorNothingReceived = "nothing received from REPL."
)

type config struct {
	APIToken        string   `json:"api_token"`
	ClojureBinPath  string   `json:"clojure_bin_path"`
	ReplHost        string   `json:"repl_host"`
	ReplPort        int      `json:"repl_port"`
	AllowedIds      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`
	IsVerbose       bool     `json:"is_verbose,omitempty"`
}

var _apiToken string
var _clojureBinPath string
var _replHost string
var _replPort int
var _monitorInterval int
var _allowedIds []string
var _isVerbose bool
var _defaultKeyboards [][]telegram.KeyboardButton

// read config file
func openConfig() (conf config, err error) {
	var exec string
	exec, err = os.Executable()
	if err == nil {
		var bytes []byte
		bytes, err = ioutil.ReadFile(filepath.Join(filepath.Dir(exec), configFilename))
		if err == nil {
			err = json.Unmarshal(bytes, &conf)
			if err == nil {
				return conf, nil
			}
		}
	}

	return config{}, err
}

func init() {
	// read config
	if conf, err := openConfig(); err != nil {
		panic(err)
	} else {
		_apiToken = conf.APIToken
		_clojureBinPath = conf.ClojureBinPath
		_replHost = conf.ReplHost
		_replPort = conf.ReplPort

		if conf.MonitorInterval <= 0 {
			conf.MonitorInterval = defaultMonitorInterval
		}
		_monitorInterval = conf.MonitorInterval
		_allowedIds = conf.AllowedIds
		_isVerbose = conf.IsVerbose
	}

	_defaultKeyboards = [][]telegram.KeyboardButton{
		[]telegram.KeyboardButton{
			telegram.KeyboardButton{
				Text: commandPublics,
			},
			telegram.KeyboardButton{
				Text: commandReset,
			},
		},
	}
}

// check if given Telegram id is allowed or not
func isAllowedID(id *string) bool {
	if id == nil {
		return false
	}

	for _, v := range _allowedIds {
		if v == *id {
			return true
		}
	}

	return false
}

func main() {
	client := repl.NewClient(_clojureBinPath, _replHost, _replPort)
	client.Verbose = _isVerbose

	// catch SIGINT and SIGTERM and terminate gracefully
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func(client *repl.Client) {
		<-sig
		client.Shutdown() // shutdown client
		os.Exit(1)
	}(client)

	bot := telegram.NewClient(_apiToken)
	bot.Verbose = _isVerbose

	// get info about this bot
	if me := bot.GetMe(); me.Ok {
		log.Printf("starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := bot.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			bot.StartMonitoringUpdates(0, _monitorInterval, func(b *telegram.Bot, update telegram.Update, err error) {
				if err == nil {
					handleUpdate(b, update, client)
				} else {
					log.Printf("error while receiving update: %s", err.Error())
				}
			})
		} else {
			panic("failed to delete webhook")
		}
	} else {
		panic("failed to get info of the bot")
	}
}

// handle received update from Telegram server
func handleUpdate(b *telegram.Bot, update telegram.Update, client *repl.Client) {
	if update.HasMessage() || update.HasEditedMessage() {
		var message *telegram.Message
		if update.HasMessage() {
			message = update.Message
		} else { // if update.HasEditedMessage() {
			message = update.EditedMessage
		}

		var msg string
		username := message.From.Username
		if !isAllowedID(username) { // check if this user is allowed to use this bot
			if username == nil {
				log.Printf("received an update from an unauthorized user: '%s'", message.From.FirstName)

				msg = fmt.Sprintf("'%s' is not allowed to use this bot.", message.From.FirstName)
			} else {
				log.Printf("received an update from an unauthorized user: @%s", *username)

				msg = fmt.Sprintf("@%s is not allowed to use this bot.", *username)
			}
		} else {
			// 'is typing...'
			b.SendChatAction(message.Chat.ID, telegram.ChatActionTyping)

			if message.HasText() {
				switch *message.Text {
				case commandStart:
					msg = messageWelcome
				case commandPublics:
					if received, err := client.Eval(repl.CommandPublics); err == nil {
						msg = repl.RespToString(received)
					} else {
						msg = messageFailedToListPublics
					}
				case commandReset:
					if received, err := client.Eval(repl.CommandReset); err == nil {
						if len(received) > 0 {
							r := received[0]
							msg = fmt.Sprintf("%s=> %s", r.Namespace, r.Value)
						} else {
							msg = messageErrorNothingReceived
						}
					} else {
						msg = messageFailedToReset
					}
				default:
					if received, err := client.Eval(*message.Text); err == nil {
						msg = repl.RespToString(received)
					} else {
						msg = fmt.Sprintf("error: %s", err)
					}
				}
			} else if message.HasDocument() {
				fileResult := b.GetFile(message.Document.FileID)
				fileURL := b.GetFileURL(*fileResult.Result)

				// download the file (as temporary)
				if filepath, err := downloadTemporarily(fileURL); err == nil {
					if received, err := client.LoadFile(filepath); err == nil {
						msg = repl.RespToString(received)

						// and delete it
						if err := os.Remove(filepath); err != nil {
							log.Printf("failed to delete file %s: %s", filepath, err)
						}
					} else {
						msg = fmt.Sprintf("failed to load file: %s", err)
					}
				} else {
					msg = fmt.Sprintf("failed to download the document: %s", err)
				}
			} else {
				msg = fmt.Sprintf("error: couldn't process your message.")
			}
		}

		// send message
		msg = strings.TrimSpace(msg)
		if msg != "" {
			if sent := b.SendMessage(message.Chat.ID, msg, map[string]interface{}{
				"reply_markup": telegram.ReplyKeyboardMarkup{ // show keyboards
					Keyboard:       _defaultKeyboards,
					ResizeKeyboard: true,
				},
			}); !sent.Ok {
				log.Printf("failed to send message: %s", *sent.Description)
			}
		}
	} else {
		log.Printf("received update has no processable message")
	}
}

// download given url
func downloadTemporarily(url string) (filepath string, err error) {
	tokens := strings.Split(url, "/")
	filename := tokens[len(tokens)-1] // get the last path segment

	filepath = path.Join(tempDir, filename)

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
