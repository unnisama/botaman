// Binary bot-auth-manual implements example of custom session storage and
// manually setting up client options without environment variables.
package main

import (
	bard "botaman/internal"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

type memorySession struct {
	mux sync.RWMutex
}

type Session struct {
	ConversationID string
	ResponseID     string
	ChoiceID       string
	NID            string
	SIDCC          string
	PSIDCC1        string
	PSIDCC3        string
}

type UserSessions map[int64]Session
type UserBards map[int64]*bard.Bard

var PSID = "dwjInIw9lZzDdUXhA9bPv25gXWoJPXXJjG-72hkBoOrSP_34E6L_rE2d2NHCEmp8wqTyYg."

// cookies can expire or regenerate so don't visit bard.google.com/<somechat>

var PSIDTS = "sidts-CjIBPVxjSiQ2CJgbCTvWb1z9NGp3FA23dKMdcub8zf1Cwyvaxkr7ndp2ZtSoc-UiyF5sexAA"

var appID = 94575
var appHash = "a3406de8d171bb422bb6ddf3bbd800e2"

// Get it from bot father.
var token = "6860877846:AAGkbKo-cH8GkqLofr2tnByDrI2tkv66UxI"

var MaxMessageSize = 3500

var aibards UserBards

var usersessions UserSessions

var workqueue chan func()

var MaxWorkQueue = 50

// memorySession implements in-memory session storage.
// Goroutine-safe.

// LoadSession loads session from memory.
func (s *memorySession) LoadSession(context.Context) ([]byte, error) {
	if s == nil {
		return nil, session.ErrNotFound
	}

	s.mux.RLock()
	defer s.mux.RUnlock()

	cpy, err := os.ReadFile("data/session.dat")

	if err != nil {
		return nil, session.ErrNotFound
	}

	return cpy, err
}

// StoreSession stores session to memory.
func (s *memorySession) StoreSession(ctx context.Context, data []byte) error {
	s.mux.Lock()
	os.WriteFile("data/session.dat", data, 0644)
	s.mux.Unlock()
	return nil
}

func SaveUserSessions() {

	if usersessions != nil {
		for k, v := range aibards {
			answer := v.GetAnswerStruct()
			usersessions[k] = Session{
				ConversationID: answer.GetConversationID(),
				ResponseID:     answer.GetResponseID(),
				ChoiceID:       answer.GetChoiceID(),
				NID:            v.NID,
				SIDCC:          v.SIDCC,
				PSIDCC1:        v.PSIDCC1,
				PSIDCC3:        v.PSIDCC3,
			}

			mdata, err := json.Marshal(usersessions)
			if err != nil {
				panic(err)
			}

			os.WriteFile("data/usersessions.dat", mdata, 0644)
		}
	}

}

func LoadUserSessions() bool {

	mdata, err := os.ReadFile("data/usersessions.dat")

	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		panic(err)
	}

	err = json.Unmarshal(mdata, &usersessions)
	if err != nil {
		panic(err)
	}

	return true
}

// func formatObject(input interface{}) string {
// 	o, ok := input.(tdp.Object)
// 	if !ok {
// 		// Handle tg.*Box values.
// 		rv := reflect.Indirect(reflect.ValueOf(input))
// 		for i := 0; i < rv.NumField(); i++ {
// 			if v, ok := rv.Field(i).Interface().(tdp.Object); ok {
// 				return formatObject(v)
// 			}
// 		}

//			return fmt.Sprintf("%T (not object)", input)
//		}
//		return tdp.Format(o)
//	}
func SplitN(str string, n int) []string {
	var strs []string
	var strlen = len(str)

	sizemax := math.Ceil(float64(strlen) / float64(n))

	for i := 0; i < int(sizemax); i++ {
		nval := n * (i + 1)
		if nval > strlen {
			nval = strlen
		}
		strs = append(strs, str[n*i:nval])
	}
	return strs

}

func main() {

	// chn := make(chan os.Signal, 1)
	// signal.Notify(chn, os.Interrupt)

	// go func() {
	// 	<-chn
	// 	SaveUserSessions()
	// 	fmt.Println("Exiting")
	// 	os.Exit(0)
	// }()

	_, err := os.Stat("data")

	if os.IsNotExist(err) {
		os.Mkdir("data", 0644)
	}
	log, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()
	ctx := context.Background()

	sessionStorage := &memorySession{}
	dispatcher := tg.NewUpdateDispatcher()
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: sessionStorage,
		Logger:         log,
		UpdateHandler:  dispatcher,
	})

	if err := client.Run(ctx, func(ctx context.Context) error {

		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}

		if !status.Authorized {
			if _, err := client.Auth().Bot(ctx, token); err != nil {
				return err
			}
		}

		api := client.API()
		sender := message.NewSender(api)
		me, err := client.Self(ctx)
		if err != nil {
			panic(err)
		}

		aibards = make(UserBards)
		usersessions = make(UserSessions)

		LoadUserSessions()

		workqueue = make(chan func(), MaxWorkQueue)

		defer close(workqueue)

		go func() {
			for work := range workqueue {
				work()
			}
		}()
		dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {

			var rmessage string
			var imessage string
			var doit bool
			var senderpeerId *tg.PeerUser
			var username = "@" + me.Username
			m, ok := update.Message.(*tg.Message)

			if !ok || m.Out {
				return nil
			}

			p, ok := m.GetPeerID().(*tg.PeerChannel)
			if !ok {
				return nil
			}

			senderpeerId, ok = m.FromID.(*tg.PeerUser)
			if !ok {
				return nil
			}
			rep, ok := m.ReplyTo.(*tg.MessageReplyHeader)
			if ok {
				// if message was a reply
				r, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
					Channel: e.Channels[p.ChannelID].AsInput(),
					ID: []tg.InputMessageClass{
						&tg.InputMessageID{
							ID: rep.ReplyToMsgID,
						},
					},
				})

				if err != nil {
					return err
				}

				ms, ok := r.AsModified()

				if !ok {
					return nil
				}

				msg, ok := ms.GetMessages()[0].(*tg.Message)

				if !ok {
					return nil
				}

				peerId, ok := msg.FromID.(*tg.PeerUser)

				if !ok {
					return nil
				}

				if peerId.UserID == me.ID {
					imessage = m.Message
					doit = true
				}
			} else {

				if strings.Contains(m.Message, username) {
					doit = true
					imessage = strings.Replace(m.Message, username, "", 1)
					imessage = strings.TrimPrefix(imessage, " ")
				}
			}

			if doit {

				var currentaibard *bard.Bard
				var currrentUserSession Session
				if currentaibard, ok = aibards[senderpeerId.UserID]; !ok {
					if currrentUserSession, ok = usersessions[senderpeerId.UserID]; ok {
						currentaibard = bard.NewUser(
							PSID,
							PSIDTS,
							currrentUserSession.ConversationID,
							currrentUserSession.ResponseID,
							currrentUserSession.ChoiceID,
							currrentUserSession.NID,
							currrentUserSession.SIDCC,
							currrentUserSession.PSIDCC1,
							currrentUserSession.PSIDCC3,
						)
					} else {
						currentaibard = bard.New(PSID, PSIDTS)
					}
					if currentaibard != nil {
						aibards[senderpeerId.UserID] = currentaibard
					}
				}

				lenQueue := len(workqueue)

				if lenQueue < MaxWorkQueue {
					estimated_time := time.Duration(lenQueue+1*6) * time.Second

					updscl, err := sender.Answer(e, update).ReplyMsg(m).Text(ctx, fmt.Sprintf("Wait ~%s (%d ahead in queue)", estimated_time.String(), lenQueue))

					if err != nil {
						panic(err)
					}

					upds, ok := updscl.(*tg.Updates)

					if !ok {
						return nil
					}

					updcl := upds.GetUpdates()[0]

					upd, ok := updcl.(*tg.UpdateMessageID)

					if !ok {
						return nil
					}

					workqueue <- func() {
						err = currentaibard.Ask(imessage)
						if err != nil {
							panic(err)
						}
						rmessage = currentaibard.GetAnswer()
						// answer := currentaibard.GetAnswerStruct()
						// fmt.Println("CID: ", answer.GetConversationID(), "\n", "RID: ", answer.GetResponseID(), "\n", "ChID: ", answer.GetChoiceID())

						for _, sval := range SplitN(rmessage, MaxMessageSize) {

							svals := strings.Split(sval, "**")
							texts := make([]message.StyledTextOption, len(svals))
							for i, v := range svals {
								if i%2 == 1 {
									texts[i] = styling.Bold(v)
								} else {
									texts[i] = styling.Plain(v)
								}
							}
							sender.Answer(e, update).ReplyMsg(m).StyledText(ctx, texts...)
						}

						api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
							Channel: e.Channels[p.ChannelID].AsInput(),
							ID:      []int{upd.ID},
						})
						SaveUserSessions()
					}
				} else {
					sender.Answer(e, update).ReplyMsg(m).Text(ctx, "Max queue limit reached. Please wait few seconds until few slot get empty and try again later")
				}
			}

			return nil
		})
		telegram.RunUntilCanceled(ctx, client)
		return nil
	}); err != nil {
		panic(err)
	}
}
