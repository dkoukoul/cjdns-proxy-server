package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
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
	referer := modifiedHeaders.Get("Referer")
	if referer != "" && strings.HasPrefix(referer, "http://["+fromHost+"]") {
		modifiedLocation := strings.ReplaceAll(referer, "http://["+fromHost+"]", "https://"+toHost)
		modifiedHeaders.Set("Referer", modifiedLocation)
	}

	return modifiedHeaders
}

func modifyResponseHeaders(response *http.Response) http.Header {
	modifiedHeaders := response.Header.Clone()

	modifiedHeaders.Del("Content-Security-Policy")
	modifiedHeaders.Del("Strict-Transport-Security")

	location := modifiedHeaders.Get("Location")
	if location != "" && strings.HasPrefix(location, "https://"+toHost) {//|| strings.HasPrefix(location, "https://yunohost.local")) {
		modifiedLocation := strings.ReplaceAll(location, "https://"+toHost, "http://["+fromHost+"]")
		//modifiedLocation = strings.ReplaceAll(modifiedLocation, "https://yunohost.local", "http://["+fromHost+"]")
		modifiedHeaders.Set("Location", modifiedLocation)
	}

	refresh := modifiedHeaders.Get("Refresh")
	if refresh != "" {
		modifiedRefresh := strings.ReplaceAll(refresh, "https://"+toHost, "http://["+fromHost+"]")
		modifiedHeaders.Set("Refresh", modifiedRefresh)
	}

	for _, cookie := range response.Cookies() {
		cookie.Domain = fromHost
		cookie.Secure = false
		modifiedHeaders.Add("Set-Cookie", cookie.String())
	}

	return modifiedHeaders
}

func modifyBody(body []byte) string {
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

func commentOut(filename string) error {
	// Open the file in read-only mode.
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read the file line by line.
	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// If the line contains "listen [::]:80", comment it out.
		if strings.Contains(line, "listen [::]:80") && !strings.HasPrefix(line, "#") {
			line = "#" + line + " # by cjdns-proxy-server"
		}
		lines = append(lines, line)
	}

	// Check for errors from scanner.
	if err := scanner.Err(); err != nil {
		return err
	}

	// Open the file in write mode.
	file, err = os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the modified lines back to the file.
	writer := bufio.NewWriter(file)
	for _, line := range lines {
		_, err := writer.WriteString(line + "\n")
		if err != nil {
			return err
		}
	}

	return writer.Flush()
}

func modifyNginxConfig() error {
	log.Println("Modifying nginx configuration")
	files, err := ioutil.ReadDir("/etc/nginx/conf.d/")
    if err != nil {
        return err
    }

    var confFiles []string
    for _, file := range files {
        if strings.HasSuffix(file.Name(), ".conf") {
            confFiles = append(confFiles, "/etc/nginx/conf.d/" + file.Name())
        }
    }
	for _, file := range confFiles {
		err := commentOut(file)
		if err != nil {
			return err
		}
	}
	// Restart nginx
	log.Println("Restarting nginx...")
	cmd := exec.Command("systemctl", "restart", "nginx")
	err = cmd.Run()
	if err != nil {
		log.Fatalf("cmd.Run() failed with %s\n", err)
	}
	// Wait until nginx has restarted
	for {
		cmd = exec.Command("systemctl", "is-active", "nginx")
		output, err := cmd.Output()
		if err != nil {
			log.Fatalf("cmd.Output() failed with %s\n", err)
		}
		if strings.TrimSpace(string(output)) == "active" {
			break
		}
		time.Sleep(1 * time.Second)
	}
	log.Println("Nginx restarted")
	return nil
}

func main() {
	data, err := os.ReadFile("/etc/hostname")
	if err != nil {
		log.Fatal("Error reading hostname file:", err)
	}
	toHost = strings.TrimSpace(string(data))

	data, err = os.ReadFile("/var/www/cjdns/cjdroute.conf")
	if err != nil {
		data, err = os.ReadFile("/etc/cjdroute.conf")
		if err != nil {
			log.Fatal("Error reading cjdroute.conf file:", err)
		}
	}

	re := regexp.MustCompile(`"ipv6":\s*"([^"]*)"`)
	match := re.FindStringSubmatch(string(data))
	if len(match) < 2 {
		log.Fatal("Error parsing cjdroute.conf file: IPv6 address not found")
	}

	fromHost := match[1]

	ip := net.ParseIP(fromHost)
	if ip == nil {
		log.Fatalf("Invalid IP address: %s", fromHost)
	}

	fromHost = ip.String()
	proxyPort = "80"
	if toHost == "" || fromHost == "" || proxyPort == "" {
		fmt.Println("One or more environment variables are not set. Please set cjdroute.conf and hostname file.")
		os.Exit(1)
	}

	modifyNginxConfig()

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
		body, err := io.ReadAll(response.Body)
		if err != nil {
			log.Println("Error reading response body:", err)
			return err
		}
		response.Body.Close()
		modifiedBody := modifyBody(body)
		response.Body = io.NopCloser(bytes.NewBufferString(modifiedBody))
		response.Header = modifyResponseHeaders(response)
		return nil
	}

	// Set up the HTTP server
	server := &http.Server{
		Addr: "[" + fromHost + "]:" + proxyPort,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("Received request: %s %s\n", r.Method, r.URL)
			r.Host = toHost
			r.Header = modifyRequestHeaders(r)
			r.URL.Host = fromHost
			r.URL.Scheme = "http"

			proxy.ServeHTTP(w, r)
		}),
	}

	// Start the server
	log.Println("Starting cjdns proxy server on port", proxyPort)
	log.Println("Proxying from", fromHost, "to", toHost)
	err = ListenAndServe(server)
	if err != nil {
		log.Println("Error starting server:", err)
	}
}
