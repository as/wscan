package wscan

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"golang.org/x/net/websocket"
)

var (
	DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/62.0.3202.94 Safari/537.36"
)

type Config struct {
	Agent string
}

type Inbox struct {
	RX   chan *Message
	conf *Config

	sess  string
	host  string
	inbox string

	ws *websocket.Conn

	stop chan bool
	err  error
}

func New(host, inbox string, conf *Config) (*Inbox, error) {
	if conf == nil {
		conf = &Config{
			Agent: DefaultUserAgent,
		}
	}
	in := &Inbox{
		RX:    make(chan *Message),
		stop:  make(chan bool),
		host:  host,
		inbox: inbox,
		conf:  conf,
	}
	err := in.mksession()
	if err != nil {
		return nil, err
	}
	if err = in.mkws(); err != nil {
		return nil, err
	}
	go in.run()
	return in, nil
}

func (in *Inbox) Close() error {
	close(in.stop)
	return in.err
}

func (in *Inbox) Next() *Message {
	return <-in.RX
}

func (in *Inbox) Err() error {
	return in.err
}

func (in *Inbox) Get(name string) (*http.Response, error) {
	h := http.Header{}
	h.Add("User-Agent", in.conf.Agent)
	h.Add("Cookie", fmt.Sprintf("JSESSIONID=%s", in.sess))
	r, err := http.NewRequest("GET", name, nil)
	if err != nil {
		return nil, fmt.Errorf("get: %s", err)
	}
	return http.DefaultClient.Do(r)
}

type Message struct {
	From string `json:"from"`
	To   string `json:"to"`
	Data struct {
		Fromfull string `json:"fromfull"`
		Headers  struct {
			Date          string `json:"date"`
			From          string `json:"from"`
			Contenttype   string `json:"contenttype"`
			Dkimsignature string `json:"dkimsignature"`
			Mimeversion   string `json:"mimeversion"`
			Subject       string `json:"subject"`
			Feedbackid    string `json:"feedbackid"`
			Messageid     string `json:"messageid"`
			Received      string `json:"received"`
			To            string `json:"to"`
			Xsesoutgoing  string `json:"xsesoutgoing"`
		} `json:"headers"`
		Subject    string `json:"subject"`
		RequestID  string `json:"requestId"`
		Origfrom   string `json:"origfrom"`
		Id         string `json:"id"`
		Time       int    `json:"time"`
		SecondsAgo int    `json:"seconds_ago"`
		Parts      []struct {
			Headers struct {
				Contenttype             string `json:"contenttype"`
				Contenttransferencoding string `json:"contenttransferencoding"`
			} `json:"headers"`
			Body string `json:"body"`
		} `json:"parts"`
	} `json:"data"`
}

func (in *Inbox) parseurl(fm string, arg ...interface{}) (u *url.URL) {
	u, in.err = url.Parse(fmt.Sprintf(fm, arg...))
	return u
}

func (in *Inbox) mkws() error {
	h := http.Header{}
	h.Add("User-Agent", in.conf.Agent)
	h.Add("Cookie", fmt.Sprintf("JSESSIONID=%s", in.sess))
	c := &websocket.Config{
		Location: in.parseurl("wss://%s/ws/fetchinbox?zone=public&query=%s", in.host, in.inbox),
		Origin:   in.parseurl("https://%s", in.host),
		Header:   h,
		Version:  13,
	}
	if in.err != nil {
		return in.err
	}
	in.ws, in.err = websocket.DialConfig(c)
	return in.err
}

func (in *Inbox) msgfmt(id interface{}) string {
	return fmt.Sprintf("https://%s/fetch_email?msgid=%s&zone=public", in.host, id)
}

func (in *Inbox) mksession() error {
	resp, err := http.Get("https://" + in.host)
	if err != nil {
		return fmt.Errorf("mksession: %s", err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "JSESSIONID" {
			in.sess = c.Value
			break
		}
	}
	if in.sess == "" {
		return fmt.Errorf("cant find JSESSION cookie in HTTP response")
	}
	return nil
}

func (in *Inbox) run() {
	var (
		data = make(map[string]interface{})
		resp *http.Response
	)
	defer close(in.RX)
	in.stop = make(chan bool)
	for in.err == nil {
		select {
		default:
			delete(data, "id")
			in.err = websocket.JSON.Receive(in.ws, &data)
			id, ok := data["id"]
			if !ok {
				continue
			}
			resp, in.err = in.Get(in.msgfmt(id))
			if in.err != nil {
				return
			}
			msg := Message{}
			json.NewDecoder(resp.Body).Decode(&msg)
			if len(msg.Data.Parts) == 0 {
				continue
			}
			in.RX <- &msg
		case <-in.stop:
			return
		}
	}
}
