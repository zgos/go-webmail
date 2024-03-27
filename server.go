//
// This is a simple WebMail project.
//

package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"text/template"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
)

var (
	//
	// The secure-cookie object we use.
	//
	cookieHandler *securecookie.SecureCookie
)

// key is the type for a context-key
//
// We use context to store the remote host (URI), username, & password
// in our session-cookie.
type key int

const (
	// keyHost stores the host URI
	keyHost key = iota

	// keyUser stores the username.
	keyUser key = iota

	// keyPass stores the password
	keyPass key = iota
)

var (
	tmpls *template.Template
)

// Data used by the frame templates, common to every page
type FrameData struct {
	Title      string
	IsLoggedIn bool
}

func loadTemplates() {
	tmpls = template.New("tmpls")
	toParse := []string{
		"data/frame-pre-content.html",
		"data/frame-post-content.html",
		"data/login.html",
		"data/folders.html",
		"data/folder-list.html",
		"data/message.html",
		"data/messages.html",
	}
	for _, file := range toParse {
		log.Printf("Parsing template %v", file)
		f, err := getResource(file)
		if err != nil {
			// Failing to load a template is a coding error
			// and can't be handled.
			log.Fatal(err)
		}
		// Successive calls to Parse allow adding more templates to the
		// same object, if they are wrapped in a {{ define }} block.
		tmpls, err = tmpls.Parse(string(f))
		if err != nil {
			// Failing to parse a template is a coding error
			// and can't be handled.
			log.Fatal(err)
		}
	}
}

// LoadCookie loads the persistent cookies from disc, if they exist.
func LoadCookie() {

	//
	// Read the hash
	//
	hash, err := ioutil.ReadFile(".cookie.hsh")
	if err == nil {

		//
		// If there was no error read the block
		//
		block, err := ioutil.ReadFile(".cookie.blk")
		if err == nil {

			//
			// And create the cookie-helper.
			//
			cookieHandler = securecookie.New(hash, block)
			return
		}
	}

	//
	// So we either failed to find, or failed to read, the existing
	// values.  (Perhaps this is the first run.)
	//
	// Generate random values.
	//
	h := securecookie.GenerateRandomKey(64)
	b := securecookie.GenerateRandomKey(32)

	//
	// Now write them out.
	//
	// If writing fails then we'll use the values, and this means
	// when the server restarts authentication will need to to be
	// repeated by the users.
	//
	// (i.e. They'll be logged out.)
	//
	err = ioutil.WriteFile(".cookie.hsh", h, 0644)
	if err != nil {
		fmt.Printf("WARNING: failed to write .cookie.hsh for persistent secure cookie")
		cookieHandler = securecookie.New(h, b)
		return
	}
	err = ioutil.WriteFile(".cookie.blk", b, 0644)
	if err != nil {
		fmt.Printf("WARNING: failed to write .cookie.blk for persistent secure cookie")
		cookieHandler = securecookie.New(h, b)
		return
	}

	//
	// Create the cookie, if we got here we've saved the data
	// for the next restart.
	//
	cookieHandler = securecookie.New(h, b)
}

// AddContext updates our HTTP-handlers to be username-aware.
func AddContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		//
		// If we have a session-cookie
		//
		if cookie, err := r.Cookie("cookie"); err == nil {

			// Make a map
			cookieValue := make(map[string]string)

			// Decode it.
			if err = cookieHandler.Decode("cookie", cookie.Value, &cookieValue); err == nil {
				//
				// Add the context to the handler, with the
				// username.
				//
				user := cookieValue["user"]
				pass := cookieValue["pass"]
				host := cookieValue["host"]
				ctx := context.WithValue(r.Context(), keyUser, user)
				ctx = context.WithValue(ctx, keyPass, pass)
				ctx = context.WithValue(ctx, keyHost, host)
				//
				// And fire it up.
				//
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

		}

		//
		// We either failed to decode the cookie, or the cookie
		// was missing.
		//
		// So we fall-back to assuming we're there is no user logged
		// in, and supply no context.
		//
		next.ServeHTTP(w, r)
		return
	})
}

// Data required for rendering the login page
type LoginData struct {
	*FrameData
	Error string
}

// loginForm shows the login-form to the user, via the template `login.html`.
func loginForm(response http.ResponseWriter, request *http.Request) {
	//
	// Lookup the HTML template
	//
	t := tmpls.Lookup("login.html")

	//
	// Execute the template into our buffer.
	//
	buf := &bytes.Buffer{}
	err := t.Execute(buf, &LoginData{&FrameData{"Login", false}, ""})

	//
	// If there were errors, then show them.
	//
	if err != nil {
		fmt.Fprintf(response, err.Error())
		return
	}

	//
	// Otherwise write the result.
	//
	buf.WriteTo(response)
}

// validate tests a login is correct.
func validate(host string, username string, password string) (bool, error) {

	x := NewIMAP(host, username, password)
	res, err := x.Connect()
	if !res {
		return false, err
	}
	if err != nil {
		return false, err
	}
	x.Close()
	return true, nil
}

// Process a login-event.
func loginHandler(response http.ResponseWriter, request *http.Request) {
	//
	// Get the hostname/username/password from the incoming submission
	//
	host := request.FormValue("host")
	user := request.FormValue("name")
	pass := request.FormValue("pass")

	//
	// If this succeeded then let the login succeed.
	//
	result, error := validate(host, user, pass)

	if result && error == nil {

		//
		// Store everything in the cookie
		//
		value := map[string]string{
			"host": host,
			"user": user,
			"pass": pass,
		}
		if encoded, err := cookieHandler.Encode("cookie", value); err == nil {
			cookie := &http.Cookie{
				Name:  "cookie",
				Value: encoded,
				Path:  "/",
			}
			http.SetCookie(response, cookie)
		}

		http.Redirect(response, request, "/folders/", 302)
		return
	}

	//
	// Create an instance of the object so we can populate
	// our template.
	//
	x := &LoginData{
		&FrameData{"Login", false},
		error.Error(),
	}

	//
	// If we reached this point there was an error with the
	// login-process.
	//
	// Load the `login.html` template, and populate it with the
	// error-message
	//
	t := tmpls.Lookup("login.html")

	//
	// Execute the template into our buffer.
	//
	buf := &bytes.Buffer{}
	err := t.Execute(buf, x)

	//
	// If there were errors, then show them.
	//
	if err != nil {
		fmt.Fprintf(response, err.Error())
		return
	}

	//
	// Otherwise write the result.
	//
	buf.WriteTo(response)
}

// indexPageHandler responds to the server-root requests.  If the user
// is logged in it will redirect them to the folder-overview, otherwise
// the login-form.
func indexPageHandler(response http.ResponseWriter, request *http.Request) {
	user := request.Context().Value(keyUser)
	if user == nil {
		http.Redirect(response, request, "/login", 302)
	}
	http.Redirect(response, request, "/folders", 302)

}

// Show the folder-list
func folderListHandler(response http.ResponseWriter, request *http.Request) {
	user := request.Context().Value(keyUser)
	pass := request.Context().Value(keyPass)
	host := request.Context().Value(keyHost)

	if user == nil || host == nil || pass == nil {
		http.Redirect(response, request, "/login", 302)
	}

	//
	// This is the page-data we'll return
	//
	type PageData struct {
		*FrameData
		Error   string
		Folders []IMAPFolder
	}

	//
	// Create an instance of the object so we can populate
	// our template.
	//
	x := &PageData{
		&FrameData{"Folders", true},
		"",
		make([]IMAPFolder, 0),
	}

	//
	// Create an IMAP object.
	//
	imap := NewIMAP(host.(string), user.(string), pass.(string))

	//
	// If we logged in then we can get the folders/messages
	//
	res, err := imap.Connect()
	if (res == true) && (err == nil) {
		x.Folders, err = imap.Folders()
		imap.Close()
		if err != nil {
			x.Error = err.Error()
		}
	} else {
		//
		// Otherwise we will show an error
		//
		x.Error = err.Error()
		imap.Close()
	}

	//
	//  Lookup the template
	//
	t := tmpls.Lookup("folders.html")

	//
	// Execute the template into our buffer.
	//
	buf := &bytes.Buffer{}
	err = t.Execute(buf, x)

	//
	// If there were errors, then show them.
	//
	if err != nil {
		fmt.Fprintf(response, err.Error())
		return
	}

	//
	// Otherwise write the result.
	//
	buf.WriteTo(response)
}

// Show the messages in the given folder.
func messageListHandler(response http.ResponseWriter, request *http.Request) {
	user := request.Context().Value(keyUser)
	pass := request.Context().Value(keyPass)
	host := request.Context().Value(keyHost)

	if user == nil || host == nil || pass == nil {
		http.Redirect(response, request, "/login", 302)
	}

	//
	// Get the name of the folder we're going to display
	//
	vars := mux.Vars(request)
	folder := vars["name"]
	start := vars["offset"]

	//
	// Start offset of paging, if any.
	//
	offset := -1
	if start != "" {
		offset, _ = strconv.Atoi(start)
	}

	//
	// This is the page-data we'll return
	//
	type PageData struct {
		*FrameData
		Error    string
		Messages []Message
		Folder   string
		Folders  []IMAPFolder

		// Previous & Next offsets for paging.  If available.
		Min  int
		Max  int
		Prev string
		Next string

		// Total/Unread counts
		Unread int
		Total  int
	}

	//
	// Create an instance of the object so we can populate
	// our template.
	//
	var x PageData
	var err error

	x.FrameData = &FrameData{html.EscapeString(folder), true}

	//
	// Fill it up
	//
	x.Folder = folder

	//
	// Create an IMAP object.
	//
	imap := NewIMAP(host.(string), user.(string), pass.(string))

	//
	// If we logged in then we can get the folders/messages
	//
	res, err := imap.Connect()
	if (res == true) && (err == nil) {
		x.Folders, err = imap.Folders()
		if err != nil {
			x.Error = err.Error()
		}
		x.Messages, x.Min, x.Max, err = imap.Messages(folder, offset)
		if err != nil {
			x.Error = err.Error()
		}

		x.Total = x.Max
		x.Unread = imap.Unread(folder)
		imap.Close()
	} else {
		//
		// Otherwise we will show an error
		//
		x.Error = err.Error()
		imap.Close()
	}

	//
	// Setup paging.
	//
	if offset < 0 {
		//
		// No offset right now.
		//
		x.Prev = fmt.Sprintf("%d", x.Max-50)
		x.Next = ""
	} else {
		//
		// We're already scrolling.
		//
		if offset > 50 {
			x.Prev = fmt.Sprintf("%d", offset-50)
		} else {
			x.Prev = "50"
		}
		if offset+50 < x.Max {
			x.Next = fmt.Sprintf("%d", offset+50)
		} else {
			x.Next = fmt.Sprintf("%d", x.Max)
		}
	}

	//
	// Lookup the messages view
	//
	t := tmpls.Lookup("messages.html")

	//
	// Execute the template into our buffer.
	//
	buf := &bytes.Buffer{}
	err = t.Execute(buf, x)

	//
	// If there were errors, then show them.
	if err != nil {
		fmt.Fprintf(response, err.Error())
		return
	}

	//
	// Otherwise write the result.
	//
	buf.WriteTo(response)
}

// Show a single message.
func messageHandler(response http.ResponseWriter, request *http.Request) {
	user := request.Context().Value(keyUser)
	pass := request.Context().Value(keyPass)
	host := request.Context().Value(keyHost)

	if user == nil || host == nil || pass == nil {
		http.Redirect(response, request, "/login", 302)
	}

	//
	// Get the name of the folder, and the number of the message
	// we're supposed to display
	//
	vars := mux.Vars(request)
	uid := vars["number"]
	folder := vars["folder"]

	//
	// This is the page-data we'll return
	//
	type PageData struct {
		*FrameData
		Error   string
		Message SingleMessage
		Folder  string
		Folders []IMAPFolder

		// Unread/Total counts
		Unread int
		Total  int
	}

	//
	// Create an instance of the object so we can populate
	// our template.
	//
	var x PageData
	var err error
	//
	// Create an IMAP object.
	//
	imap := NewIMAP(host.(string), user.(string), pass.(string))

	//
	// If we logged in then we can get the folders/messages
	//
	res, err := imap.Connect()
	if (res == true) && (err == nil) {
		x.Folders, err = imap.Folders()
		if err != nil {
			x.Error = err.Error()
		}
		x.Message, err = imap.GetMessage(uid, folder)
		if err != nil {
			x.Error = err.Error()
		}

		x.Total = x.Message.Total
		x.Unread = x.Message.Unread
		imap.Close()
	} else {
		//
		// Otherwise we will show an error
		//
		x.Error = err.Error()
		imap.Close()
	}

	x.Folder = folder

	// Render the title into a string and generate the frame data
	x.FrameData = &FrameData{html.EscapeString("Message " + folder + " [" + uid + "]"), true}

	//
	// Lookup the the message view template
	//
	t := tmpls.Lookup("message.html")

	//
	// Execute the template into our buffer.
	//
	buf := &bytes.Buffer{}
	err = t.Execute(buf, x)

	//
	// If there were errors, then show them.
	if err != nil {
		fmt.Fprintf(response, err.Error())
		return
	}

	//
	// Otherwise write the result.
	//
	buf.WriteTo(response)
}

// Download an attachment
func attachmentHandler(response http.ResponseWriter, request *http.Request) {
	user := request.Context().Value(keyUser)
	pass := request.Context().Value(keyPass)
	host := request.Context().Value(keyHost)

	if user == nil || host == nil || pass == nil {
		http.Redirect(response, request, "/login", 302)
	}

	//
	// Get the name of the folder, and the number of the message
	// we're supposed to display
	//
	vars := mux.Vars(request)
	uid := vars["number"]
	folder := vars["folder"]
	filename := vars["filename"]

	//
	// Create an IMAP object.
	//
	imap := NewIMAP(host.(string), user.(string), pass.(string))

	//
	// The message we'll parse.
	//
	var msg SingleMessage

	//
	// If we logged in then we can get the folders/messages
	//
	res, err := imap.Connect()
	if (res == true) && (err == nil) {
		msg, err = imap.GetMessage(uid, folder)
		imap.Close()
		if err != nil {
			fmt.Fprintf(response, "Error getting message - %s\n", err.Error())
			return
		}
	} else {
		//
		// Otherwise we will show an error
		//
		fmt.Fprintf(response, "Error getting message - %s\n", err.Error())
		imap.Close()
		return
	}

	//
	// Now loop over the attachments
	//
	for _, e := range msg.Attachments {
		if e.FileName == filename {

			//
			// Set the content-type
			//
			response.Header().Set("Content-Type", e.ContentType)
			response.Write(e.Content)

			return
		}
	}

	//
	// Failed to find attachment
	//
	fmt.Fprintf(response, "Failed to find attachment")
}

// logout handler
func logoutHandler(response http.ResponseWriter, request *http.Request) {
	cookie := &http.Cookie{
		Name:   "cookie",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	}
	http.SetCookie(response, cookie)
	http.Redirect(response, request, "/", 302)
}

// main is our entry-point
func main() {
	log.SetFlags(log.Lmicroseconds | log.LstdFlags | log.Llongfile)

	//
	// Load our HTML templates
	//
	loadTemplates()

	//
	// Configure our secure cookies
	//
	LoadCookie()

	//
	// Configure our routes.
	//
	var router = mux.NewRouter()
	router.HandleFunc("/", indexPageHandler)

	router.HandleFunc("/login", loginForm).Methods("GET")
	router.HandleFunc("/login/", loginForm).Methods("GET")
	router.HandleFunc("/login", loginHandler).Methods("POST")
	router.HandleFunc("/login/", loginHandler).Methods("POST")

	router.HandleFunc("/logout", logoutHandler).Methods("GET")
	router.HandleFunc("/logout/", logoutHandler).Methods("GET")
	router.HandleFunc("/logout", logoutHandler).Methods("POST")
	router.HandleFunc("/logout/", logoutHandler).Methods("POST")

	//
	// Folder List
	//
	router.HandleFunc("/folders", folderListHandler).Methods("GET")
	router.HandleFunc("/folders/", folderListHandler).Methods("GET")

	//
	// List of messages in the given folder.
	//
	router.HandleFunc("/folder/{name}/{offset}", messageListHandler).Methods("GET")
	router.HandleFunc("/folder/{name}/{offset}/", messageListHandler).Methods("GET")
	router.HandleFunc("/folder/{name}", messageListHandler).Methods("GET")
	router.HandleFunc("/folder/{name}/", messageListHandler).Methods("GET")

	//
	// Single message
	//
	router.HandleFunc("/message/{number}/{folder}", messageHandler).Methods("GET")
	router.HandleFunc("/message/{number}/{folder}/", messageHandler).Methods("GET")

	//
	// Attachment download
	//
	router.HandleFunc("/attach/{folder}/{number}/{filename}", attachmentHandler).Methods("GET")
	router.HandleFunc("/attach/{folder}/{number}/{filename}/", attachmentHandler).Methods("GET")

	http.Handle("/", router)

	//
	// Show what we're going to bind upon.
	//
	bindHost := "127.0.0.1"
	bindPort := 8080

	bind := fmt.Sprintf("%s:%d", bindHost, bindPort)
	fmt.Printf("Listening on http://%s/\n", bind)

	//
	// Wire up logging.
	//
	loggedRouter := handlers.LoggingHandler(os.Stdout, router)

	//
	// Wire up context (i.e. cookie-based session stuff.)
	//
	contextRouter := AddContext(loggedRouter)

	//
	// We want to make sure we handle timeouts effectively
	//
	srv := &http.Server{
		Addr:         bind,
		Handler:      contextRouter,
		ReadTimeout:  25 * time.Second,
		IdleTimeout:  25 * time.Second,
		WriteTimeout: 25 * time.Second,
	}

	//
	// Launch the server.
	//
	err := srv.ListenAndServe()
	if err != nil {
		fmt.Printf("\nError starting HTTP server: %s\n", err.Error())
	}
}

// Return the contents of a resource.
func getResource(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf(err.Error())
	}
	return io.ReadAll(f)
}
