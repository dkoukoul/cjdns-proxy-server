package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	toHost    string
	fromHost  string
	proxyPort string
)

func modifyRequestHeaders(request *http.Request) http.Header {
	modifiedHeaders := request.Header.Clone()
	modifiedHeaders.Set("X-Real-IP", request.RemoteAddr)
	modifiedHeaders.Set("X-Forwarded-For", request.RemoteAddr)
	modifiedHeaders.Set("Referer", request.Referer())
	modifiedHeaders.Set("I-Set-The-Referer-To", request.Referer())
	return modifiedHeaders
}

func modifyResponseHeaders(response *http.Response) http.Header {
	modifiedHeaders := response.Header.Clone()

	// Delete the headers
	modifiedHeaders.Del("Content-Security-Policy")
	modifiedHeaders.Del("Strict-Transport-Security")

	// Modify the Location and Refresh headers
	location := modifiedHeaders.Get("Location")
	if location != "" && strings.HasPrefix(location, "https://"+toHost) {
		modifiedLocation := strings.ReplaceAll(location, "https://"+toHost, "http://["+fromHost+"]")
		modifiedHeaders.Set("Location", modifiedLocation)
	}

	refresh := modifiedHeaders.Get("Refresh")
	if refresh != "" {
		modifiedRefresh := strings.ReplaceAll(refresh, "https://"+toHost, "http://["+fromHost+"]")
		modifiedHeaders.Set("Refresh", modifiedRefresh)
	}

	// Modify the Set-Cookie headers
	for _, cookie := range response.Cookies() {
		cookie.Domain = fromHost
		cookie.Secure = false
		modifiedHeaders.Add("Set-Cookie", cookie.String())
	}

	return modifiedHeaders
}

func modifyBody(body []byte) string {
	// Perform the replacement
	modifiedBody := strings.ReplaceAll(string(body), "https://"+toHost, "http://["+fromHost+"]")
	return modifiedBody
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	if c, err = ln.AcceptTCP(); err != nil {
		return
	}
	c.(*net.TCPConn).SetKeepAlive(true)
	c.(*net.TCPConn).SetKeepAlivePeriod(3 * time.Minute)
	return
}

func ListenAndServe(server *http.Server) error {
	addr := server.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp6", addr)
	if err != nil {
		return err
	}
	return server.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)})
}

func main() {

	toHost = os.Getenv("PROXY_TO_HOST")
	fromHost = os.Getenv("PROXY_FROM_HOST")
	proxyPort = os.Getenv("PROXY_PORT")
	if toHost == "" || fromHost == "" || proxyPort == "" {
		fmt.Println("One or more environment variables are not set. Please set PROXY_TO_HOST, PROXY_FROM_HOST, and PROXY_PORT.")
		os.Exit(1)
	}
	// Create a reverse proxy
	target, _ := url.Parse("https://127.0.0.1")
	proxy := httputil.NewSingleHostReverseProxy(target)

	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13, // Use TLSv1.3
			InsecureSkipVerify: true,             // Turn off SSL verification
			ServerName:         toHost,           // Set the SSL server name
		},
	}

	// Modify the request before sending it to the backend
	proxy.ModifyResponse = func(response *http.Response) error {
		log.Printf("Sending response: %s\n", response.Status)
		// Read the response body
		body, err := io.ReadAll(response.Body)
		if err != nil {
			log.Println("Error reading response body:", err)
			return err
		}

		// Close the original body
		response.Body.Close()

		// Perform the replacement
		modifiedBody := modifyBody(body)

		// Write the modified body back
		response.Body = io.NopCloser(bytes.NewBufferString(modifiedBody))

		response.Header = modifyResponseHeaders(response)

		return nil
	}

	// Set up the HTTP server
	server := &http.Server{
		Addr: "["+fromHost+"]:" + proxyPort,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("Received request: %s %s\n", r.Method, r.URL)
			// Modify request headers
			r.Host = toHost
			r.Header = modifyRequestHeaders(r)

			// Rewrite request URL
			r.URL.Host = fromHost
			r.URL.Scheme = "http"

			// Perform reverse proxying
			proxy.ServeHTTP(w, r)
		}),
	}

	// Start the server
	log.Println("Starting server on port", proxyPort)
	err := ListenAndServe(server)
	if err != nil {
		log.Println("Error starting server:", err)
	}
}
