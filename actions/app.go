package actions

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/gobuffalo/buffalo"
	"github.com/gobuffalo/buffalo-pop/pop/popmw"
	"github.com/gobuffalo/envy"
	csrf "github.com/gobuffalo/mw-csrf"
	forcessl "github.com/gobuffalo/mw-forcessl"
	i18n "github.com/gobuffalo/mw-i18n"
	paramlogger "github.com/gobuffalo/mw-paramlogger"
	"github.com/gobuffalo/packr/v2"
	"github.com/iansmith/mazarin/models"
	"github.com/unrolled/secure"
)

// ENV is used to help switch settings based on where the
// application is being run. Default is "development".
var ENV = envy.Get("GO_ENV", "development")
var app *buffalo.App

//T is the locale-based translator.
var T *i18n.Translator

const (
	//SlackSignature is their HMAC-SHA256 with my key
	SlackSignature = "X-Slack-Signature"
	//SlackTimestamp is when the request was sent to prevent replays
	SlackTimestamp = "X-Slack-Request-Timestamp"
)

// App is where all routes and middleware for buffalo
// should be defined. This is the nerve center of your
// application.
//
// Routing, middleware, groups, etc... are declared TOP -> DOWN.
// This means if you add a middleware to `app` *after* declaring a
// group, that group will NOT have that new middleware. The same
// is true of resource declarations as well.
//
// It also means that routes are checked in the order they are declared.
// `ServeFiles` is a CATCH-ALL route, so it should always be
// placed last in the route declarations, as it will prevent routes
// declared after it to never be called.
func App() *buffalo.App {
	if app == nil {
		app = buffalo.New(buffalo.Options{
			Env:         ENV,
			SessionName: "_mazarin_session",
			//		PreHandlers: []http.Handler{&SlackHandler{}},
		})

		//app.Muxer().HandleFunc("/slack/slashCommand", HandleSlackSlashCommand)
		// Automatically redirect to SSL
		app.Use(forceSSL())

		// Log request parameters (filters apply).
		app.Use(paramlogger.ParameterLogger)

		// Protect against CSRF attacks. https://www.owasp.org/index.php/Cross-Site_Request_Forgery_(CSRF)
		// Remove to disable this.
		app.Use(csrf.New)

		// Wraps each request in a transaction.
		//  c.Value("tx").(*pop.Connection)
		// Remove to disable this.
		app.Use(popmw.Transaction(models.DB))

		// Setup and use translations:
		app.Use(translations())

		app.GET("/", HomeHandler)
		app.ServeFiles("/", assetsBox) // serve files from the public directory

	}

	return app
}

//
// func rawMiddleware(next buffalo.Handler) buffalo.Handler {
// 	return func(c buffalo.Context) error {
// 		var buf bytes.Buffer
// 		n, err := io.Copy(&buf, c.Request().Body)
// 		c.Logger().Debugf("aaaa %d copied\n", n)
// 		if err != nil {
// 			c.Logger().Fatalf("whoa! can't copy! error was %s\n", err.Error())
// 		}
// 		c.Logger().Debugf("before handler %+v\n %s,(%d)\n", c.Request(), buf.String(), buf.Len())
// 		err = next(c)
// 		return err
// 	}
// }

// translations will load locale files, set up the translator `actions.T`,
// and will return a middleware to use to load the correct locale for each
// request.
// for more information: https://gobuffalo.io/en/docs/localization
func translations() buffalo.MiddlewareFunc {
	var err error
	if T, err = i18n.New(packr.New("app:locales", "../locales"), "en-US"); err != nil {
		app.Stop(err)
	}
	return T.Middleware()
}

// forceSSL will return a middleware that will redirect an incoming request
// if it is not HTTPS. "http://example.com" => "https://example.com".
// This middleware does **not** enable SSL. for your application. To do that
// we recommend using a proxy: https://gobuffalo.io/en/docs/proxy
// for more information: https://github.com/unrolled/secure/
func forceSSL() buffalo.MiddlewareFunc {
	return forcessl.Middleware(secure.Options{
		SSLRedirect:     ENV == "production",
		SSLProxyHeaders: map[string]string{"X-Forwarded-Proto": "https"},
	})
}

// SlashCommand is created in response to a slash command coming from slack. By
// the time this is created, the 200 has already been returned to slack.  The
// request has already been validated.
type SlashCommand struct {
	token       string
	teamID      string
	teamDomain  string
	channelID   string
	channelName string
	userID      string
	userName    string
	command     string
	text        string
	responseURL string
	triggerID   string
}

//SlackHandler is used for testing slack integration.
type SlackHandler struct{}

// this produces
// 2018/12/03 15:00:23 Request Body is empty (and size of form data is 11)

func (s *SlackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, r.Body)
	if err != nil {
		log.Fatalf("error during body copy: %v", err)
	}
	if n == 0 {
		log.Fatalf("Request Body is empty (and size of form data is %d)\n", len(r.Form))
	}
	HandleSlackSlashCommand(w, r)
}

// HandleSlackSlashCommand is the primary entry point for slash commands coming from slack.
// If the command is correctly validated, the parsed out command is passed to
// a SlashCommand object.
func HandleSlackSlashCommand(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	io.Copy(&buf, r.Body)

	timeSent := fmt.Sprintf("%s", r.Header.Get(SlackTimestamp))
	signature := fmt.Sprintf("%s", r.Header.Get(SlackSignature))
	if timeSent != "" && signature != "" {
		i, err := strconv.ParseInt(timeSent, 10, 64)
		if err != nil {
			log.Printf("unable to process time sent %s\n", timeSent)
			w.WriteHeader(400)
			w.Write([]byte("cannot parse time sent"))
			return
		}
		if time.Now().Sub(time.Unix(i, 0)).Minutes() > 5.0 {
			log.Printf("time sent is too old %v\n", time.Now().Sub(time.Unix(i, 0)).Minutes())
			w.WriteHeader(400)
			w.Write([]byte("time sent is too old"))
			return

		}
		content := &bytes.Buffer{}
		content.WriteString("v0:" + timeSent + ":" + buf.String())
		signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
		sig, err := hex.DecodeString(signature[3:])
		if err != nil {
			log.Fatalf("unable to decode " + signature[3:])
		}
		if checkMAC(content.Bytes(), sig, []byte(signingSecret)) {
			//at this point, we have an ok message
			v, err := url.ParseQuery(buf.String())
			if err != nil {
				log.Printf("cannot parse form data on correctly signed message\n")
				w.WriteHeader(400)
				w.Write([]byte("cannot parse form data on correctly signed message"))
				return
			}
			s := &SlashCommand{
				token:       v.Get("token"),
				teamID:      v.Get("team_id"),
				teamDomain:  v.Get("team_domain"),
				channelID:   v.Get("channel_id"),
				channelName: v.Get("channel_name"),
				userID:      v.Get("user_id"),
				userName:    v.Get("user_name"),
				command:     v.Get("command"),
				text:        v.Get("text"),
				responseURL: v.Get("response_url"),
				triggerID:   v.Get("trigger_id"),
			}
			w.WriteHeader(200)
			s.Process()
		} else {
			log.Printf("signature failed check\n")
			w.WriteHeader(400)
			w.Write([]byte("signature failed check"))
			return
		}
	} else {
		log.Printf("missing header field necessary for HMAC check\n")
		w.WriteHeader(400)
		w.Write([]byte("missing header field necessary for HMAC check"))
		return
	}

}

// checkMAC reports whether messageMAC is a valid HMAC tag for message.
func checkMAC(message, messageMAC, key []byte) bool {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	expectedMAC := mac.Sum(nil)
	return hmac.Equal(messageMAC, expectedMAC)
}

//Process is where we handle the commands coming from the slack side.
func (s *SlashCommand) Process() {
	fmt.Printf("cmd is '%s'\n\tuser is %s[%s], team is %s, channel is %s[%s]\n", s.text,
		s.userName, s.userID, s.teamID, s.channelName, s.channelID)
}
