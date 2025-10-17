package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"

	"github.com/gorilla/websocket"
	"github.com/urfave/cli/v3"
)

func main() {
	cmd := cli.Command{
		Name:   "wscat",
		Usage:  "cat, but for websockets",
		Action: ActionMain,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "origin",
				Value:   "samehost",
				Usage:   "URL to use for the origin header ('samehost' is special)",
				Sources: cli.EnvVars("WSCAT_ORIGIN"),
			},
			&cli.StringSliceFlag{
				Name:    "header",
				Aliases: []string{"H"},
				Usage:   "headers to pass to the remote",
				Sources: cli.EnvVars("WSCAT_HEADER"),
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

var RegexParseHeader = regexp.MustCompile("^\\s*([^\\:]+)\\s*:\\s*(.*)$")

func MustParseHeader(header string) (string, string) {
	if !RegexParseHeader.MatchString(header) {
		log.Fatalf("Unable to parse header: %v (re: %v)", header,
			RegexParseHeader.String())
		return "", ""
	}

	parts := RegexParseHeader.FindStringSubmatch(header)
	return parts[1], parts[2]
}

func MustParseHeaders(c *cli.Command) http.Header {
	headers := http.Header{}

	for _, h := range c.StringSlice("header") {
		key, value := MustParseHeader(h)
		headers.Set(key, value)
	}

	return headers
}

func MustParseURL(u string) *url.URL {
	tgt, err := url.ParseRequestURI(u)
	if err != nil {
		log.Fatalf("Unable to parse URL: %v: %v", u, err)
	}
	switch tgt.Scheme {
	case "http":
		tgt.Scheme = "ws"
	case "https":
		tgt.Scheme = "wss"
	}
	return tgt
}

func ActionMain(_ context.Context, c *cli.Command) error {

	args := c.Args()

	if args.Len() < 1 {
		log.Fatalf("usage: wscat <url>")
	}

	urlString := args.First()

	u := MustParseURL(urlString)

	headers := MustParseHeaders(c)
	origin := c.String("origin")
	if origin == "samehost" {
		origin = "//" + u.Host
	}
	headers.Set("Origin", origin)

	if u.User != nil {
		userPassBytes := []byte(u.User.String() + ":")
		token := base64.StdEncoding.EncodeToString(userPassBytes)
		headers.Set("Authorization", fmt.Sprintf("Basic %v", token))
		u.User = nil
	}

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			err = fmt.Errorf("%v: response: %v", err, resp.Status)
		}
		log.Fatalf("Error dialing: %v", err)
	}
	defer conn.Close()

	errc := make(chan error)

	go func() {
		// _, err := io.Copy(os.Stdout, conn)
		var (
			err error
			r   io.Reader
		)
		for {
			_, r, err = conn.NextReader()
			if err != nil {
				break
			}
			_, err = io.Copy(os.Stdout, r)
			if err != nil {
				break
			}
		}
		if err != io.EOF {
			log.Printf("Error copying to stdout: %v", err)
		}
		errc <- err
	}()

	go func() {
		var (
			err error
			w   io.Writer
		)

		for {
			w, err = conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				break
			}
			_, err = io.Copy(w, os.Stdin)
			if err != nil {
				break
			}

			break
		}

		if err != nil && err != io.EOF {
			log.Printf("Error copying from stdin: %v", err)
		}

		errc <- err
	}()

	<-errc
	return nil
}
