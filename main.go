/*

rtop-bot - remote system monitoring bot

Copyright (c) 2015 RapidLoop

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

package main

import (
	"fmt"
	"github.com/daneharrigan/hipchat"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const (
	VERSION = "0.1"
)

var sshUsername, idRsaPath string

//----------------------------------------------------------------------------

func main() {

	if len(os.Args) != 3 {
		fmt.Printf(
			`rtop-bot %s - (c) 2015 RapidLoop - http://www.rtop-monitor.org/rtop-bot
rtop-bot is a HipChat bot that can do remote system monitoring over SSH

Usage: rtop-bot userJid roomJid

where
  userJid is the HipChat user jabber ID, like 139999_999914
  roomJid is the HipChat room jabber ID, like 139999_opschat
`, VERSION)
		os.Exit(1)
	}

	log.SetPrefix("rtop-bot: ")
	log.SetFlags(0)

	username := os.Args[1]
	roomjid := os.Args[2]
	if strings.HasSuffix(username, "@chat.hipchat.com") {
		username = strings.Replace(username, "@chat.hipchat.com", "", 1)
	}
	if !strings.HasSuffix(roomjid, "@conf.hipchat.com") {
		roomjid += "@conf.hipchat.com"
	}
	pass, err := getpass("Password for user \"" + username + "\": ")
	if err != nil {
		log.Print(err)
	}

	// get default username for SSH connections
	usr, err := user.Current()
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	sshUsername = usr.Username

	// expand ~/.ssh/id_rsa and check if it exists
	idRsaPath = filepath.Join(usr.HomeDir, ".ssh", "id_rsa")
	if _, err := os.Stat(idRsaPath); os.IsNotExist(err) {
		idRsaPath = ""
	}

	// expand ~/.ssh/config and parse if it exists
	sshConfig := filepath.Join(usr.HomeDir, ".ssh", "config")
	if _, err := os.Stat(sshConfig); err == nil {
		parseSshConfig(sshConfig)
	}

	client, err := hipchat.NewClient(username, pass, "bot")
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}

	nick, mname := getUserInfo(client, username)

	client.Status("chat")
	client.Join(roomjid, nick)
	log.Printf("[%s] now serving room [%s]", nick, roomjid)
	log.Print("hit ^C to exit")

	go client.KeepAlive()
	for message := range client.Messages() {
		if strings.HasPrefix(message.Body, "@"+mname) {
			go client.Say(roomjid, nick, process(message.Body))
		}
	}
}

func getUserInfo(client *hipchat.Client, id string) (string, string) {
	id = id + "@chat.hipchat.com"
	client.RequestUsers()
	select {
	case users := <-client.Users():
		for _, user := range users {
			if user.Id == id {
				log.Printf("using username [%s] and mention name [%s]",
					user.Name, user.MentionName)
				return user.Name, user.MentionName
			}
		}
	case <-time.After(10 * time.Second):
		log.Print("timed out waiting for user list")
		os.Exit(1)
	}
	return "rtop-bot", "rtop-bot"
}

func process(request string) string {

	parts := strings.Fields(request)
	if len(parts) != 3 || parts[1] != "status" {
		return "say \"status <hostname>\" to see vital stats of <hostname>"
	}

	address, user, keypath := getSshEntryOrDefault(parts[2])
	client, err := sshConnect(user, address, keypath)
	if err != nil {
		return fmt.Sprintf("[%s]: %v", parts[2], err)
	}

	stats := Stats{}
	getAllStats(client, &stats)
	result := fmt.Sprintf(
		`[%s] up %s, load %s %s %s, procs %s running of %s total
[%s] mem: %s of %s free, swap %s of %s free
`,
		stats.Hostname, fmtUptime(&stats), stats.Load1, stats.Load5,
		stats.Load10, stats.RunningProcs, stats.TotalProcs,
		stats.Hostname, fmtBytes(stats.MemFree), fmtBytes(stats.MemTotal),
		fmtBytes(stats.SwapFree), fmtBytes(stats.SwapTotal),
	)
	if len(stats.FSInfos) > 0 {
		for _, fs := range stats.FSInfos {
			result += fmt.Sprintf("[%s] fs %s: %s of %s free\n",
				stats.Hostname,
				fs.MountPoint,
				fmtBytes(fs.Free),
				fmtBytes(fs.Used+fs.Free),
			)
		}
	}
	return result
}
