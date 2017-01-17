package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
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
		log.Printf("Starting bot: @%s (%s)\n", *me.Result.Username, *me.Result.FirstName)

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := bot.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			bot.StartMonitoringUpdates(0, _monitorInterval, func(b *telegram.Bot, update telegram.Update, err error) {
				if err == nil {
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
							log.Printf("*** Received an update from an unauthorized user: @%s\n", username)

							str = fmt.Sprintf("Your id: @%s is not allowed to use this bot.", username)
						} else {
							if message.HasText() {
								// 'is typing...'
								b.SendChatAction(message.Chat.Id, telegram.ChatActionTyping)

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
										strs := []string{}
										if received.HasError() { // nREPL error
											// join status strings
											for _, s := range received.Status {
												strs = append(strs, fmt.Sprintf("%v", s))
											}
											status := strings.Join(strs, ", ")

											// show statuses and exceptions
											if received.Ex == received.RootEx {
												str = fmt.Sprintf("%s: %s\n", status, received.Ex)
											} else {
												str = fmt.Sprintf("%s: %s (%s)\n", status, received.Ex, received.RootEx)
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
											str = strings.Join(strs, "\n")
										}
									} else {
										str = fmt.Sprintf("Error: %s", err)
									}
								}
							} else {
								str = fmt.Sprintf("Error: couldn't process your message.")
							}
						}

						// send message
						if sent := b.SendMessage(message.Chat.Id, &str, map[string]interface{}{
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
							log.Printf("*** Failed to send message: %s\n", *sent.Description)
						}
					} else {
						log.Printf("*** Received update has no message\n")
					}
				} else {
					log.Printf("*** Error while receiving update (%s)\n", err.Error())
				}
			})
		} else {
			panic("Failed to delete webhook")
		}
	} else {
		panic("Failed to get info of the bot")
	}
}
