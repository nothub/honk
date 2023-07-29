//
// Copyright (c) 2019 Ted Unangst <tedu@tedunangst.com>
//
// Permission to use, copy, modify, and distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"html"
	"io"
	notrand "math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"humungus.tedunangst.com/r/webs/cache"
	"humungus.tedunangst.com/r/webs/gate"
	"humungus.tedunangst.com/r/webs/httpsig"
	"humungus.tedunangst.com/r/webs/junk"
	"humungus.tedunangst.com/r/webs/templates"
)

var theonetruename = `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`
var thefakename = `application/activity+json`
var falsenames = []string{
	`application/ld+json`,
	`application/activity+json`,
}
var itiswhatitis = "https://www.w3.org/ns/activitystreams"
var thewholeworld = "https://www.w3.org/ns/activitystreams#Public"

var fastTimeout time.Duration = 5
var slowTimeout time.Duration = 30

func friendorfoe(ct string) bool {
	ct = strings.ToLower(ct)
	for _, at := range falsenames {
		if strings.HasPrefix(ct, at) {
			return true
		}
	}
	return false
}

var develClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	},
}

func PostJunk(keyname string, key httpsig.PrivateKey, url string, j junk.Junk) error {
	return PostMsg(keyname, key, url, j.ToBytes())
}

func PostMsg(keyname string, key httpsig.PrivateKey, url string, msg []byte) error {
	client := http.DefaultClient
	if develMode {
		client = develClient
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(msg))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "honksnonk/5.0; "+serverName)
	req.Header.Set("Content-Type", theonetruename)
	httpsig.SignRequest(keyname, key, req, msg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*slowTimeout*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 201:
	case 202:
	default:
		return fmt.Errorf("http post status: %d", resp.StatusCode)
	}
	ilog.Printf("successful post: %s %d", url, resp.StatusCode)
	return nil
}

func GetJunk(userid int64, url string) (junk.Junk, error) {
	return GetJunkTimeout(userid, url, slowTimeout*time.Second)
}

func GetJunkFast(userid int64, url string) (junk.Junk, error) {
	return GetJunkTimeout(userid, url, fastTimeout*time.Second)
}

func GetJunkHardMode(userid int64, url string) (junk.Junk, error) {
	j, err := GetJunk(userid, url)
	if err != nil {
		emsg := err.Error()
		if emsg == "http get status: 502" || strings.Contains(emsg, "timeout") {
			ilog.Printf("trying again after error: %s", emsg)
			time.Sleep(time.Duration(60+notrand.Int63n(60)) * time.Second)
			j, err = GetJunk(userid, url)
			if err != nil {
				ilog.Printf("still couldn't get it")
			} else {
				ilog.Printf("retry success!")
			}
		}
	}
	return j, err
}

var flightdeck = gate.NewSerializer()

var signGets = true

func junkGet(userid int64, url string, args junk.GetArgs) (junk.Junk, error) {
	client := http.DefaultClient
	if args.Client != nil {
		client = args.Client
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if args.Accept != "" {
		req.Header.Set("Accept", args.Accept)
	}
	if args.Agent != "" {
		req.Header.Set("User-Agent", args.Agent)
	}
	if signGets {
		var ki *KeyInfo
		ok := ziggies.Get(userid, &ki)
		if ok {
			httpsig.SignRequest(ki.keyname, ki.seckey, req, nil)
		}
	}
	if args.Timeout != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), args.Timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
	case 201:
	case 202:
	default:
		return nil, fmt.Errorf("http get status: %d", resp.StatusCode)
	}
	return junk.Read(resp.Body)
}

func GetJunkTimeout(userid int64, url string, timeout time.Duration) (junk.Junk, error) {
	client := http.DefaultClient
	if develMode {
		client = develClient
	}
	fn := func() (interface{}, error) {
		at := thefakename
		if strings.Contains(url, ".well-known/webfinger?resource") {
			at = "application/jrd+json"
		}
		j, err := junkGet(userid, url, junk.GetArgs{
			Accept:  at,
			Agent:   "honksnonk/5.0; " + serverName,
			Timeout: timeout,
			Client:  client,
		})
		return j, err
	}

	ji, err := flightdeck.Call(url, fn)
	if err != nil {
		return nil, err
	}
	j := ji.(junk.Junk)
	return j, nil
}

func fetchsome(url string) ([]byte, error) {
	client := http.DefaultClient
	if develMode {
		client = develClient
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		ilog.Printf("error fetching %s: %s", url, err)
		return nil, err
	}
	req.Header.Set("User-Agent", "honksnonk/5.0; "+serverName)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		ilog.Printf("error fetching %s: %s", url, err)
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 201:
	case 202:
	default:
		return nil, fmt.Errorf("http get not 200: %d %s", resp.StatusCode, url)
	}
	var buf bytes.Buffer
	limiter := io.LimitReader(resp.Body, 10*1024*1024)
	io.Copy(&buf, limiter)
	return buf.Bytes(), nil
}

func savedonk(url string, name, desc, media string, localize bool) *Donk {
	if url == "" {
		return nil
	}
	if donk := finddonk(url); donk != nil {
		return donk
	}
	ilog.Printf("saving donk: %s", url)
	data := []byte{}
	if localize {
		fn := func() (interface{}, error) {
			return fetchsome(url)
		}
		ii, err := flightdeck.Call(url, fn)
		if err != nil {
			ilog.Printf("error fetching donk: %s", err)
			localize = false
			goto saveit
		}
		data = ii.([]byte)

		if len(data) == 10*1024*1024 {
			ilog.Printf("truncation likely")
		}
		if strings.HasPrefix(media, "image") {
			img, err := shrinkit(data)
			if err != nil {
				ilog.Printf("unable to decode image: %s", err)
				localize = false
				data = []byte{}
				goto saveit
			}
			data = img.Data
			media = "image/" + img.Format
		} else if media == "application/pdf" {
			if len(data) > 1000000 {
				ilog.Printf("not saving large pdf")
				localize = false
				data = []byte{}
			}
		} else if len(data) > 100000 {
			ilog.Printf("not saving large attachment")
			localize = false
			data = []byte{}
		}
	}
saveit:
	fileid, err := savefile(name, desc, url, media, localize, data)
	if err != nil {
		elog.Printf("error saving file %s: %s", url, err)
		return nil
	}
	donk := new(Donk)
	donk.FileID = fileid
	return donk
}

func iszonked(userid int64, xid string) bool {
	var id int64
	row := stmtFindZonk.QueryRow(userid, xid)
	err := row.Scan(&id)
	if err == nil {
		return true
	}
	if err != sql.ErrNoRows {
		ilog.Printf("error querying zonk: %s", err)
	}
	return false
}

func needxonk(user *WhatAbout, x *Honk) bool {
	if rejectxonk(x) {
		return false
	}
	return needxonkid(user, x.XID)
}
func needbonkid(user *WhatAbout, xid string) bool {
	return needxonkidX(user, xid, true)
}
func needxonkid(user *WhatAbout, xid string) bool {
	return needxonkidX(user, xid, false)
}
func needxonkidX(user *WhatAbout, xid string, isannounce bool) bool {
	if !strings.HasPrefix(xid, "https://") {
		return false
	}
	if strings.HasPrefix(xid, user.URL+"/") {
		return false
	}
	if rejectorigin(user.ID, xid, isannounce) {
		ilog.Printf("rejecting origin: %s", xid)
		return false
	}
	if iszonked(user.ID, xid) {
		ilog.Printf("already zonked: %s", xid)
		return false
	}
	var id int64
	row := stmtFindXonk.QueryRow(user.ID, xid)
	err := row.Scan(&id)
	if err == nil {
		return false
	}
	if err != sql.ErrNoRows {
		ilog.Printf("error querying xonk: %s", err)
	}
	return true
}

func eradicatexonk(userid int64, xid string) {
	xonk := getxonk(userid, xid)
	if xonk != nil {
		deletehonk(xonk.ID)
	}
	_, err := stmtSaveZonker.Exec(userid, xid, "zonk")
	if err != nil {
		elog.Printf("error eradicating: %s", err)
	}
}

func savexonk(x *Honk) {
	ilog.Printf("saving xonk: %s", x.XID)
	go handles(x.Honker)
	go handles(x.Oonker)
	savehonk(x)
}

type Box struct {
	In     string
	Out    string
	Shared string
}

var boxofboxes = cache.New(cache.Options{Filler: func(ident string) (*Box, bool) {
	var info string
	row := stmtGetXonker.QueryRow(ident, "boxes")
	err := row.Scan(&info)
	if err != nil {
		dlog.Printf("need to get boxes for %s", ident)
		var j junk.Junk
		j, err = GetJunk(readyLuserOne, ident)
		if err != nil {
			dlog.Printf("error getting boxes: %s", err)
			return nil, false
		}
		allinjest(originate(ident), j)
		row = stmtGetXonker.QueryRow(ident, "boxes")
		err = row.Scan(&info)
	}
	if err == nil {
		m := strings.Split(info, " ")
		b := &Box{In: m[0], Out: m[1], Shared: m[2]}
		return b, true
	}
	return nil, false
}})

func gimmexonks(user *WhatAbout, outbox string) {
	dlog.Printf("getting outbox: %s", outbox)
	j, err := GetJunk(user.ID, outbox)
	if err != nil {
		ilog.Printf("error getting outbox: %s", err)
		return
	}
	t, _ := j.GetString("type")
	origin := originate(outbox)
	if t == "OrderedCollection" {
		items, _ := j.GetArray("orderedItems")
		if items == nil {
			items, _ = j.GetArray("items")
		}
		if items == nil {
			obj, ok := j.GetMap("first")
			if ok {
				items, _ = obj.GetArray("orderedItems")
			} else {
				page1, ok := j.GetString("first")
				if ok {
					j, err = GetJunk(user.ID, page1)
					if err != nil {
						ilog.Printf("error getting page1: %s", err)
						return
					}
					items, _ = j.GetArray("orderedItems")
				}
			}
		}
		if len(items) > 20 {
			items = items[0:20]
		}
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		for _, item := range items {
			obj, ok := item.(junk.Junk)
			if ok {
				xonksaver(user, obj, origin)
				continue
			}
			xid, ok := item.(string)
			if ok {
				if !needxonkid(user, xid) {
					continue
				}
				obj, err = GetJunk(user.ID, xid)
				if err != nil {
					ilog.Printf("error getting item: %s", err)
					continue
				}
				xonksaver(user, obj, originate(xid))
			}
		}
	}
}

func newphone(a []string, obj junk.Junk) []string {
	for _, addr := range []string{"to", "cc", "attributedTo"} {
		who, _ := obj.GetString(addr)
		if who != "" {
			a = append(a, who)
		}
		whos, _ := obj.GetArray(addr)
		for _, w := range whos {
			who, _ := w.(string)
			if who != "" {
				a = append(a, who)
			}
		}
	}
	return a
}

func extractattrto(obj junk.Junk) string {
	who, _ := obj.GetString("attributedTo")
	if who != "" {
		return who
	}
	o, ok := obj.GetMap("attributedTo")
	if ok {
		id, ok := o.GetString("id")
		if ok {
			return id
		}
	}
	arr, _ := obj.GetArray("attributedTo")
	for _, a := range arr {
		o, ok := a.(junk.Junk)
		if ok {
			t, _ := o.GetString("type")
			id, _ := o.GetString("id")
			if t == "Person" || t == "" {
				return id
			}
		}
		s, ok := a.(string)
		if ok {
			return s
		}
	}
	return ""
}

func firstofmany(obj junk.Junk, key string) string {
	if val, _ := obj.GetString(key); val != "" {
		return val
	}
	if arr, _ := obj.GetArray(key); len(arr) > 0 {
		val, ok := arr[0].(string)
		if ok {
			return val
		}
	}
	return ""
}

var re_mast0link = regexp.MustCompile(`https://[[:alnum:].]+/users/[[:alnum:]]+/statuses/[[:digit:]]+`)
var re_masto1ink = regexp.MustCompile(`https://([[:alnum:].]+)/@([[:alnum:]]+)/([[:digit:]]+)`)
var re_misslink = regexp.MustCompile(`https://[[:alnum:].]+/notes/[[:alnum:]]+`)
var re_honklink = regexp.MustCompile(`https://[[:alnum:].]+/u/[[:alnum:]]+/h/[[:alnum:]]+`)
var re_r0malink = regexp.MustCompile(`https://[[:alnum:].]+/objects/[[:alnum:]-]+`)
var re_roma1ink = regexp.MustCompile(`https://[[:alnum:].]+/notice/[[:alnum:]]+`)
var re_qtlinks = regexp.MustCompile(`>https://[^\s<]+<`)

func xonksaver(user *WhatAbout, item junk.Junk, origin string) *Honk {
	depth := 0
	maxdepth := 10
	currenttid := ""
	goingup := 0
	var xonkxonkfn func(item junk.Junk, origin string, isUpdate bool) *Honk

	qutify := func(user *WhatAbout, content string) string {
		if depth >= maxdepth {
			ilog.Printf("in too deep")
			return content
		}
		// well this is gross
		malcontent := strings.ReplaceAll(content, `</span><span class="ellipsis">`, "")
		malcontent = strings.ReplaceAll(malcontent, `</span><span class="invisible">`, "")
		mlinks := re_qtlinks.FindAllString(malcontent, -1)
		for _, m := range mlinks {
			tryit := false
			m = m[1 : len(m)-1]
			if re_mast0link.MatchString(m) || re_misslink.MatchString(m) ||
				re_honklink.MatchString(m) || re_r0malink.MatchString(m) ||
				re_roma1ink.MatchString(m) {
				tryit = true
			} else if re_masto1ink.MatchString(m) {
				m = re_masto1ink.ReplaceAllString(m, "https://$1/users/$2/statuses/$3")
				tryit = true
			}
			if tryit {
				if x := getxonk(user.ID, m); x != nil {
					content = fmt.Sprintf("%s<blockquote>%s</blockquote>", content, x.Noise)
				} else if j, err := GetJunk(user.ID, m); err == nil {
					q, ok := j.GetString("content")
					if ok {
						content = fmt.Sprintf("%s<blockquote>%s</blockquote>", content, q)
					}
					prevdepth := depth
					depth = maxdepth
					xonkxonkfn(j, originate(m), false)
					depth = prevdepth
				}
			}
		}
		return content
	}

	saveonemore := func(xid string) {
		dlog.Printf("getting onemore: %s", xid)
		if depth >= maxdepth {
			ilog.Printf("in too deep")
			return
		}
		obj, err := GetJunkHardMode(user.ID, xid)
		if err != nil {
			ilog.Printf("error getting onemore: %s: %s", xid, err)
			return
		}
		depth++
		xonkxonkfn(obj, originate(xid), false)
		depth--
	}

	xonkxonkfn = func(item junk.Junk, origin string, isUpdate bool) *Honk {
		id, _ := item.GetString("id")
		what := firstofmany(item, "type")
		dt, ok := item.GetString("published")
		if !ok {
			dt = time.Now().Format(time.RFC3339)
		}

		var err error
		var xid, rid, url, convoy string
		var replies []string
		var obj junk.Junk
		waspage := false
		preferorig := false
		switch what {
		case "Delete":
			obj, ok = item.GetMap("object")
			if ok {
				xid, _ = obj.GetString("id")
			} else {
				xid, _ = item.GetString("object")
			}
			if xid == "" {
				return nil
			}
			if originate(xid) != origin {
				ilog.Printf("forged delete: %s", xid)
				return nil
			}
			ilog.Printf("eradicating %s", xid)
			eradicatexonk(user.ID, xid)
			return nil
		case "Remove":
			xid, _ = item.GetString("object")
			targ, _ := obj.GetString("target")
			ilog.Printf("remove %s from %s", obj, targ)
			return nil
		case "Tombstone":
			xid, _ = item.GetString("id")
			if xid == "" {
				return nil
			}
			if originate(xid) != origin {
				ilog.Printf("forged delete: %s", xid)
				return nil
			}
			ilog.Printf("eradicating %s", xid)
			eradicatexonk(user.ID, xid)
			return nil
		case "Announce":
			obj, ok = item.GetMap("object")
			if ok {
				what, ok := obj.GetString("type")
				if ok && what == "Create" {
					obj, ok = obj.GetMap("object")
					if !ok {
						ilog.Printf("lost object inside create %s", id)
						return nil
					}
					what, _ = obj.GetString("type")
				}
				if what == "Page" {
					waspage = true
				}
				xid, _ = obj.GetString("id")
			} else {
				xid, _ = item.GetString("object")
			}
			if !needbonkid(user, xid) {
				return nil
			}
			origin = originate(xid)
			if ok && originate(id) == origin {
				dlog.Printf("using object in announce for %s", xid)
			} else {
				dlog.Printf("getting bonk: %s", xid)
				obj, err = GetJunkHardMode(user.ID, xid)
				if err != nil {
					ilog.Printf("error getting bonk: %s: %s", xid, err)
				}
			}
			what = "bonk"
		case "Update":
			isUpdate = true
			fallthrough
		case "Create":
			obj, ok = item.GetMap("object")
			if !ok {
				xid, _ = item.GetString("object")
				dlog.Printf("getting created honk: %s", xid)
				if originate(xid) != origin {
					ilog.Printf("out of bounds %s not from %s", xid, origin)
					return nil
				}
				obj, err = GetJunkHardMode(user.ID, xid)
				if err != nil {
					ilog.Printf("error getting creation: %s", err)
				}
			}
			if obj == nil {
				ilog.Printf("no object for creation %s", id)
				return nil
			}
			return xonkxonkfn(obj, origin, isUpdate)
		case "Read":
			xid, ok = item.GetString("object")
			if ok {
				if !needxonkid(user, xid) {
					dlog.Printf("don't need read obj: %s", xid)
					return nil
				}
				obj, err = GetJunkHardMode(user.ID, xid)
				if err != nil {
					ilog.Printf("error getting read: %s", err)
					return nil
				}
				return xonkxonkfn(obj, originate(xid), false)
			}
			return nil
		case "Add":
			xid, ok = item.GetString("object")
			if ok {
				// check target...
				if !needxonkid(user, xid) {
					dlog.Printf("don't need added obj: %s", xid)
					return nil
				}
				obj, err = GetJunkHardMode(user.ID, xid)
				if err != nil {
					ilog.Printf("error getting add: %s", err)
					return nil
				}
				return xonkxonkfn(obj, originate(xid), false)
			}
			return nil
		case "Move":
			obj = item
			what = "move"
		case "Page":
			waspage = true
			fallthrough
		case "Audio":
			fallthrough
		case "Image":
			if what == "Image" {
				preferorig = true
			}
			fallthrough
		case "Video":
			fallthrough
		case "Question":
			fallthrough
		case "Note":
			fallthrough
		case "Article":
			obj = item
			what = "honk"
		case "Event":
			obj = item
			what = "event"
		case "ChatMessage":
			obj = item
			what = "chonk"
		default:
			ilog.Printf("unknown activity: %s", what)
			dumpactivity(item)
			return nil
		}

		if obj != nil {
			xid, _ = obj.GetString("id")
		}

		if xid == "" {
			ilog.Printf("don't know what xid is")
			item.Write(ilog.Writer())
			return nil
		}
		if originate(xid) != origin {
			if !develMode && origin != "" {
				ilog.Printf("original sin: %s not from %s", xid, origin)
				item.Write(ilog.Writer())
				return nil
			}
		}

		var xonk Honk
		// early init
		xonk.XID = xid
		xonk.UserID = user.ID
		xonk.Honker, _ = item.GetString("actor")
		if xonk.Honker == "" {
			xonk.Honker, _ = item.GetString("attributedTo")
		}
		if obj != nil {
			if xonk.Honker == "" {
				xonk.Honker = extractattrto(obj)
			}
			xonk.Oonker = extractattrto(obj)
			if xonk.Oonker == xonk.Honker {
				xonk.Oonker = ""
			}
			xonk.Audience = newphone(nil, obj)
		}
		xonk.Audience = append(xonk.Audience, xonk.Honker)
		xonk.Audience = oneofakind(xonk.Audience)
		xonk.Public = loudandproud(xonk.Audience)

		var mentions []Mention
		if obj != nil {
			ot, _ := obj.GetString("type")
			url, _ = obj.GetString("url")
			if dt2, ok := obj.GetString("published"); ok {
				dt = dt2
			}
			content, _ := obj.GetString("content")
			if !strings.HasPrefix(content, "<p>") {
				content = "<p>" + content
			}
			precis, _ := obj.GetString("summary")
			if name, ok := obj.GetString("name"); ok {
				if precis != "" {
					content = precis + "<p>" + content
				}
				precis = html.EscapeString(name)
			}
			if sens, _ := obj["sensitive"].(bool); sens && precis == "" {
				precis = "unspecified horror"
			}
			if waspage {
				content += fmt.Sprintf(`<p><a href="%s">%s</a>`, url, url)
				url = xid
			}
			if user.Options.InlineQuotes {
				content = qutify(user, content)
			}
			rid, ok = obj.GetString("inReplyTo")
			if !ok {
				if robj, ok := obj.GetMap("inReplyTo"); ok {
					rid, _ = robj.GetString("id")
				}
			}
			convoy, _ = obj.GetString("context")
			if convoy == "" {
				convoy, _ = obj.GetString("conversation")
			}
			if ot == "Question" {
				if what == "honk" {
					what = "qonk"
				}
				content += "<ul>"
				ans, _ := obj.GetArray("oneOf")
				for _, ai := range ans {
					a, ok := ai.(junk.Junk)
					if !ok {
						continue
					}
					as, _ := a.GetString("name")
					content += "<li>" + as
				}
				ans, _ = obj.GetArray("anyOf")
				for _, ai := range ans {
					a, ok := ai.(junk.Junk)
					if !ok {
						continue
					}
					as, _ := a.GetString("name")
					content += "<li>" + as
				}
				content += "</ul>"
			}
			if ot == "Move" {
				targ, _ := obj.GetString("target")
				content += string(templates.Sprintf(`<p>Moved to <a href="%s">%s</a>`, targ, targ))
			}
			if len(content) > 90001 {
				ilog.Printf("content too long. truncating")
				content = content[:90001]
			}

			xonk.Noise = content
			xonk.Precis = precis
			if rejectxonk(&xonk) {
				dlog.Printf("fast reject: %s", xid)
				return nil
			}

			numatts := 0
			procatt := func(att junk.Junk) {
				at, _ := att.GetString("type")
				mt, _ := att.GetString("mediaType")
				if mt == "" {
					mt = "image"
				}
				u, ok := att.GetString("url")
				if !ok {
					u, ok = att.GetString("href")
				}
				if !ok {
					if ua, ok := att.GetArray("url"); ok && len(ua) > 0 {
						u, ok = ua[0].(string)
						if !ok {
							if uu, ok := ua[0].(junk.Junk); ok {
								u, _ = uu.GetString("href")
								if mt == "" {
									mt, _ = uu.GetString("mediaType")
								}
							}
						}
					} else if uu, ok := att.GetMap("url"); ok {
						u, _ = uu.GetString("href")
						if mt == "" {
							mt, _ = uu.GetString("mediaType")
						}
					}
				}
				name, _ := att.GetString("name")
				desc, _ := att.GetString("summary")
				desc = html.UnescapeString(desc)
				if desc == "" {
					desc = name
				}
				localize := false
				if at == "Document" || at == "Image" {
					mt = strings.ToLower(mt)
					dlog.Printf("attachment: %s %s", mt, u)
					if mt == "text/plain" || mt == "application/pdf" ||
						strings.HasPrefix(mt, "image") {
						if numatts > 4 {
							ilog.Printf("excessive attachment: %s", at)
						} else {
							localize = true
						}
					}
				} else if at == "Link" {
					if waspage {
						xonk.Noise += fmt.Sprintf(`<p><a href="%s">%s</a>`, u, u)
						return
					}
					if name == "" {
						name = u
					}
				} else {
					ilog.Printf("unknown attachment: %s", at)
				}
				if skipMedia(&xonk) {
					localize = false
				}
				if preferorig && !localize {
					return
				}
				donk := savedonk(u, name, desc, mt, localize)
				if donk != nil {
					xonk.Donks = append(xonk.Donks, donk)
				}
				numatts++
			}
			if img, ok := obj.GetMap("image"); ok {
				procatt(img)
			}
			if preferorig {
				atts, _ := obj.GetArray("url")
				for _, atti := range atts {
					att, ok := atti.(junk.Junk)
					if !ok {
						ilog.Printf("attachment that wasn't map?")
						continue
					}
					procatt(att)
				}
				if numatts == 0 {
					preferorig = false
				}
			}
			if !preferorig {
				atts, _ := obj.GetArray("attachment")
				for _, atti := range atts {
					att, ok := atti.(junk.Junk)
					if !ok {
						ilog.Printf("attachment that wasn't map?")
						continue
					}
					procatt(att)
				}
				if att, ok := obj.GetMap("attachment"); ok {
					procatt(att)
				}
			}
			proctag := func(tag junk.Junk) {
				tt, _ := tag.GetString("type")
				name, _ := tag.GetString("name")
				desc, _ := tag.GetString("summary")
				desc = html.UnescapeString(desc)
				if desc == "" {
					desc = name
				}
				if tt == "Emoji" {
					icon, _ := tag.GetMap("icon")
					mt, _ := icon.GetString("mediaType")
					if mt == "" {
						mt = "image/png"
					}
					u, _ := icon.GetString("url")
					donk := savedonk(u, name, desc, mt, true)
					if donk != nil {
						xonk.Donks = append(xonk.Donks, donk)
					}
				}
				if tt == "Hashtag" {
					if name == "" || name == "#" {
						// skip it
					} else {
						if name[0] != '#' {
							name = "#" + name
						}
						xonk.Onts = append(xonk.Onts, name)
					}
				}
				if tt == "Place" {
					p := new(Place)
					p.Name = name
					p.Latitude, _ = tag.GetNumber("latitude")
					p.Longitude, _ = tag.GetNumber("longitude")
					p.Url, _ = tag.GetString("url")
					xonk.Place = p
				}
				if tt == "Mention" {
					var m Mention
					m.Who, _ = tag.GetString("name")
					m.Where, _ = tag.GetString("href")
					mentions = append(mentions, m)
				}
			}
			tags, _ := obj.GetArray("tag")
			for _, tagi := range tags {
				tag, ok := tagi.(junk.Junk)
				if !ok {
					continue
				}
				proctag(tag)
			}
			tag, ok := obj.GetMap("tag")
			if ok {
				proctag(tag)
			}
			if starttime, ok := obj.GetString("startTime"); ok {
				if start, err := time.Parse(time.RFC3339, starttime); err == nil {
					t := new(Time)
					t.StartTime = start
					endtime, _ := obj.GetString("endTime")
					t.EndTime, _ = time.Parse(time.RFC3339, endtime)
					dura, _ := obj.GetString("duration")
					if strings.HasPrefix(dura, "PT") {
						dura = strings.ToLower(dura[2:])
						d, _ := time.ParseDuration(dura)
						t.Duration = Duration(d)
					}
					xonk.Time = t
				}
			}
			if loca, ok := obj.GetMap("location"); ok {
				if tt, _ := loca.GetString("type"); tt == "Place" {
					p := new(Place)
					p.Name, _ = loca.GetString("name")
					p.Latitude, _ = loca.GetNumber("latitude")
					p.Longitude, _ = loca.GetNumber("longitude")
					p.Url, _ = loca.GetString("url")
					xonk.Place = p
				}
			}

			xonk.Onts = oneofakind(xonk.Onts)
			replyobj, ok := obj.GetMap("replies")
			if ok {
				items, ok := replyobj.GetArray("items")
				if !ok {
					first, ok := replyobj.GetMap("first")
					if ok {
						items, _ = first.GetArray("items")
					}
				}
				for _, repl := range items {
					s, ok := repl.(string)
					if ok {
						replies = append(replies, s)
					}
				}
			}

		}

		if currenttid == "" {
			currenttid = convoy
		}

		// init xonk
		xonk.What = what
		xonk.RID = rid
		xonk.Date, _ = time.Parse(time.RFC3339, dt)
		xonk.URL = url
		xonk.Format = "html"
		xonk.Convoy = convoy
		xonk.Mentions = mentions
		for _, m := range mentions {
			if m.Where == user.URL {
				xonk.Whofore = 1
			}
		}
		imaginate(&xonk)

		if what == "chonk" {
			ch := Chonk{
				UserID: xonk.UserID,
				XID:    xid,
				Who:    xonk.Honker,
				Target: xonk.Honker,
				Date:   xonk.Date,
				Noise:  xonk.Noise,
				Format: xonk.Format,
				Donks:  xonk.Donks,
			}
			savechonk(&ch)
			return nil
		}

		if isUpdate {
			dlog.Printf("something has changed! %s", xonk.XID)
			prev := getxonk(user.ID, xonk.XID)
			if prev == nil {
				ilog.Printf("didn't find old version for update: %s", xonk.XID)
				isUpdate = false
			} else {
				xonk.ID = prev.ID
				updatehonk(&xonk)
			}
		}
		if !isUpdate && needxonk(user, &xonk) {
			if rid != "" && xonk.Public {
				if needxonkid(user, rid) {
					goingup++
					saveonemore(rid)
					goingup--
				}
				if convoy == "" {
					xx := getxonk(user.ID, rid)
					if xx != nil {
						convoy = xx.Convoy
					}
				}
			}
			if convoy == "" {
				convoy = currenttid
			}
			if convoy == "" {
				convoy = "data:,missing-" + xfiltrate()
				currenttid = convoy
			}
			xonk.Convoy = convoy
			savexonk(&xonk)
		}
		if goingup == 0 {
			for _, replid := range replies {
				if needxonkid(user, replid) {
					dlog.Printf("missing a reply: %s", replid)
					saveonemore(replid)
				}
			}
		}
		return &xonk
	}

	return xonkxonkfn(item, origin, false)
}

func dumpactivity(item junk.Junk) {
	fd, err := os.OpenFile("savedinbox.json", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		elog.Printf("error opening inbox! %s", err)
		return
	}
	defer fd.Close()
	item.Write(fd)
	io.WriteString(fd, "\n")
}

func rubadubdub(user *WhatAbout, req junk.Junk) {
	actor, _ := req.GetString("actor")
	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL + "/dub/" + xfiltrate()
	j["type"] = "Accept"
	j["actor"] = user.URL
	j["to"] = actor
	j["published"] = time.Now().UTC().Format(time.RFC3339)
	j["object"] = req

	deliverate(user.ID, actor, j.ToBytes())
}

func itakeitallback(user *WhatAbout, xid string, owner string, folxid string) {
	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL + "/unsub/" + folxid
	j["type"] = "Undo"
	j["actor"] = user.URL
	j["to"] = owner
	f := junk.New()
	f["id"] = user.URL + "/sub/" + folxid
	f["type"] = "Follow"
	f["actor"] = user.URL
	f["to"] = owner
	f["object"] = xid
	j["object"] = f
	j["published"] = time.Now().UTC().Format(time.RFC3339)

	deliverate(user.ID, owner, j.ToBytes())
}

func subsub(user *WhatAbout, xid string, owner string, folxid string) {
	if xid == "" {
		ilog.Printf("can't subscribe to empty")
		return
	}
	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL + "/sub/" + folxid
	j["type"] = "Follow"
	j["actor"] = user.URL
	j["to"] = owner
	j["object"] = xid
	j["published"] = time.Now().UTC().Format(time.RFC3339)

	deliverate(user.ID, owner, j.ToBytes())
}

func activatedonks(donks []*Donk) []junk.Junk {
	var atts []junk.Junk
	for _, d := range donks {
		if re_emus.MatchString(d.Name) {
			continue
		}
		jd := junk.New()
		jd["mediaType"] = d.Media
		jd["name"] = d.Name
		jd["summary"] = html.EscapeString(d.Desc)
		jd["type"] = "Document"
		jd["url"] = d.URL
		atts = append(atts, jd)
	}
	return atts
}

// returns activity, object
func jonkjonk(user *WhatAbout, h *Honk) (junk.Junk, junk.Junk) {
	dt := h.Date.Format(time.RFC3339)
	var jo junk.Junk
	j := junk.New()
	j["id"] = user.URL + "/" + h.What + "/" + shortxid(h.XID)
	j["actor"] = user.URL
	j["published"] = dt
	j["to"] = h.Audience[0]
	if len(h.Audience) > 1 {
		j["cc"] = h.Audience[1:]
	}

	switch h.What {
	case "update":
		fallthrough
	case "event":
		fallthrough
	case "honk":
		j["type"] = "Create"
		jo = junk.New()
		jo["id"] = h.XID
		jo["type"] = "Note"
		if h.What == "event" {
			jo["type"] = "Event"
		}
		if h.What == "update" {
			j["type"] = "Update"
			jo["updated"] = dt
		}
		jo["published"] = dt
		jo["url"] = h.XID
		jo["attributedTo"] = user.URL
		if h.RID != "" {
			jo["inReplyTo"] = h.RID
		}
		if h.Convoy != "" {
			jo["context"] = h.Convoy
			jo["conversation"] = h.Convoy
		}
		jo["to"] = h.Audience[0]
		if len(h.Audience) > 1 {
			jo["cc"] = h.Audience[1:]
		}
		if !h.Public {
			jo["directMessage"] = true
		}
		translate(h)
		redoimages(h)
		if h.Precis != "" {
			jo["sensitive"] = true
		}

		var replies []string
		for _, reply := range h.Replies {
			replies = append(replies, reply.XID)
		}
		if len(replies) > 0 {
			jr := junk.New()
			jr["type"] = "Collection"
			jr["totalItems"] = len(replies)
			jr["items"] = replies
			jo["replies"] = jr
		}

		var tags []junk.Junk
		for _, m := range h.Mentions {
			t := junk.New()
			t["type"] = "Mention"
			t["name"] = m.Who
			t["href"] = m.Where
			tags = append(tags, t)
		}
		for _, o := range h.Onts {
			t := junk.New()
			t["type"] = "Hashtag"
			o = strings.ToLower(o)
			t["href"] = fmt.Sprintf("https://%s/o/%s", serverName, o[1:])
			t["name"] = o
			tags = append(tags, t)
		}
		for _, e := range herdofemus(h.Noise) {
			t := junk.New()
			t["id"] = e.ID
			t["type"] = "Emoji"
			t["name"] = e.Name
			i := junk.New()
			i["type"] = "Image"
			i["mediaType"] = e.Type
			i["url"] = e.ID
			t["icon"] = i
			tags = append(tags, t)
		}
		for _, e := range fixupflags(h) {
			t := junk.New()
			t["id"] = e.ID
			t["type"] = "Emoji"
			t["name"] = e.Name
			i := junk.New()
			i["type"] = "Image"
			i["mediaType"] = "image/png"
			i["url"] = e.ID
			t["icon"] = i
			tags = append(tags, t)
		}
		if len(tags) > 0 {
			jo["tag"] = tags
		}
		if p := h.Place; p != nil {
			t := junk.New()
			t["type"] = "Place"
			if p.Name != "" {
				t["name"] = p.Name
			}
			if p.Latitude != 0 {
				t["latitude"] = p.Latitude
			}
			if p.Longitude != 0 {
				t["longitude"] = p.Longitude
			}
			if p.Url != "" {
				t["url"] = p.Url
			}
			jo["location"] = t
		}
		if t := h.Time; t != nil {
			jo["startTime"] = t.StartTime.Format(time.RFC3339)
			if t.Duration != 0 {
				jo["duration"] = "PT" + strings.ToUpper(t.Duration.String())
			}
		}
		atts := activatedonks(h.Donks)
		if len(atts) > 0 {
			jo["attachment"] = atts
		}
		jo["summary"] = h.Precis
		jo["content"] = h.Noise
		j["object"] = jo
	case "bonk":
		j["type"] = "Announce"
		if h.Convoy != "" {
			j["context"] = h.Convoy
		}
		j["object"] = h.XID
	case "unbonk":
		b := junk.New()
		b["id"] = user.URL + "/" + "bonk" + "/" + shortxid(h.XID)
		b["type"] = "Announce"
		b["actor"] = user.URL
		if h.Convoy != "" {
			b["context"] = h.Convoy
		}
		b["object"] = h.XID
		j["type"] = "Undo"
		j["object"] = b
	case "zonk":
		j["type"] = "Delete"
		j["object"] = h.XID
	case "ack":
		j["type"] = "Read"
		j["object"] = h.XID
		if h.Convoy != "" {
			j["context"] = h.Convoy
		}
	case "react":
		j["type"] = "EmojiReact"
		j["object"] = h.XID
		if h.Convoy != "" {
			j["context"] = h.Convoy
		}
		j["content"] = h.Noise
	case "deack":
		b := junk.New()
		b["id"] = user.URL + "/" + "ack" + "/" + shortxid(h.XID)
		b["type"] = "Read"
		b["actor"] = user.URL
		b["object"] = h.XID
		if h.Convoy != "" {
			b["context"] = h.Convoy
		}
		j["type"] = "Undo"
		j["object"] = b
	}

	return j, jo
}

var oldjonks = cache.New(cache.Options{Filler: func(xid string) ([]byte, bool) {
	row := stmtAnyXonk.QueryRow(xid)
	honk := scanhonk(row)
	if honk == nil || !honk.Public {
		return nil, true
	}
	user, _ := butwhatabout(honk.Username)
	rawhonks := gethonksbyconvoy(honk.UserID, honk.Convoy, 0)
	reversehonks(rawhonks)
	for _, h := range rawhonks {
		if h.RID == honk.XID && h.Public && (h.Whofore == 2 || h.IsAcked()) {
			honk.Replies = append(honk.Replies, h)
		}
	}
	donksforhonks([]*Honk{honk})
	_, j := jonkjonk(user, honk)
	if j == nil {
		elog.Fatalf("what just happened? %v", honk)
	}
	j["@context"] = itiswhatitis

	return j.ToBytes(), true
}, Limit: 128})

func gimmejonk(xid string) ([]byte, bool) {
	var j []byte
	ok := oldjonks.Get(xid, &j)
	return j, ok
}

func boxuprcpts(user *WhatAbout, addresses []string, useshared bool) map[string]bool {
	rcpts := make(map[string]bool)
	for _, a := range addresses {
		if a == "" || a == thewholeworld || a == user.URL || strings.HasSuffix(a, "/followers") {
			continue
		}
		if a[0] == '%' {
			rcpts[a] = true
			continue
		}
		var box *Box
		ok := boxofboxes.Get(a, &box)
		if ok && useshared && box.Shared != "" {
			rcpts["%"+box.Shared] = true
		} else {
			rcpts[a] = true
		}
	}
	return rcpts
}

func chonkifymsg(user *WhatAbout, ch *Chonk) []byte {
	dt := ch.Date.Format(time.RFC3339)
	aud := []string{ch.Target}

	jo := junk.New()
	jo["id"] = ch.XID
	jo["type"] = "ChatMessage"
	jo["published"] = dt
	jo["attributedTo"] = user.URL
	jo["to"] = aud
	jo["content"] = ch.HTML
	atts := activatedonks(ch.Donks)
	if len(atts) > 0 {
		jo["attachment"] = atts
	}
	var tags []junk.Junk
	for _, e := range herdofemus(ch.Noise) {
		t := junk.New()
		t["id"] = e.ID
		t["type"] = "Emoji"
		t["name"] = e.Name
		i := junk.New()
		i["type"] = "Image"
		i["mediaType"] = e.Type
		i["url"] = e.ID
		t["icon"] = i
		tags = append(tags, t)
	}
	if len(tags) > 0 {
		jo["tag"] = tags
	}

	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL + "/" + "honk" + "/" + shortxid(ch.XID)
	j["type"] = "Create"
	j["actor"] = user.URL
	j["published"] = dt
	j["to"] = aud
	j["object"] = jo

	return j.ToBytes()
}

func sendchonk(user *WhatAbout, ch *Chonk) {
	msg := chonkifymsg(user, ch)

	rcpts := make(map[string]bool)
	rcpts[ch.Target] = true
	for a := range rcpts {
		go deliverate(user.ID, a, msg)
	}
}

func honkworldwide(user *WhatAbout, honk *Honk) {
	jonk, _ := jonkjonk(user, honk)
	jonk["@context"] = itiswhatitis
	msg := jonk.ToBytes()

	rcpts := boxuprcpts(user, honk.Audience, honk.Public)

	if honk.Public {
		for _, h := range getdubs(user.ID) {
			if h.XID == user.URL {
				continue
			}
			var box *Box
			ok := boxofboxes.Get(h.XID, &box)
			if ok && box.Shared != "" {
				rcpts["%"+box.Shared] = true
			} else {
				rcpts[h.XID] = true
			}
		}
		for _, f := range getbacktracks(honk.XID) {
			if f[0] == '%' {
				rcpts[f] = true
			} else {
				var box *Box
				ok := boxofboxes.Get(f, &box)
				if ok && box.Shared != "" {
					rcpts["%"+box.Shared] = true
				} else {
					rcpts[f] = true
				}
			}
		}
	}
	for a := range rcpts {
		go deliverate(user.ID, a, msg)
	}
	if honk.Public && len(honk.Onts) > 0 {
		collectiveaction(honk)
	}
}

func collectiveaction(honk *Honk) {
	user := getserveruser()
	for _, ont := range honk.Onts {
		dubs := getnameddubs(readyLuserOne, ont)
		if len(dubs) == 0 {
			continue
		}
		j := junk.New()
		j["@context"] = itiswhatitis
		j["type"] = "Add"
		j["id"] = user.URL + "/add/" + shortxid(ont+honk.XID)
		j["actor"] = user.URL
		j["object"] = honk.XID
		j["target"] = fmt.Sprintf("https://%s/o/%s", serverName, ont[1:])
		rcpts := make(map[string]bool)
		for _, dub := range dubs {
			var box *Box
			ok := boxofboxes.Get(dub.XID, &box)
			if ok && box.Shared != "" {
				rcpts["%"+box.Shared] = true
			} else {
				rcpts[dub.XID] = true
			}
		}
		msg := j.ToBytes()
		for a := range rcpts {
			go deliverate(user.ID, a, msg)
		}
	}
}

func junkuser(user *WhatAbout) junk.Junk {
	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL
	j["inbox"] = user.URL + "/inbox"
	j["outbox"] = user.URL + "/outbox"
	j["name"] = user.Display
	j["preferredUsername"] = user.Name
	j["summary"] = user.HTAbout
	var tags []junk.Junk
	for _, o := range user.Onts {
		t := junk.New()
		t["type"] = "Hashtag"
		o = strings.ToLower(o)
		t["href"] = fmt.Sprintf("https://%s/o/%s", serverName, o[1:])
		t["name"] = o
		tags = append(tags, t)
	}
	if len(tags) > 0 {
		j["tag"] = tags
	}

	if user.ID > 0 {
		j["type"] = "Person"
		j["url"] = user.URL
		j["followers"] = user.URL + "/followers"
		j["following"] = user.URL + "/following"
		a := junk.New()
		a["type"] = "Image"
		a["mediaType"] = "image/png"
		a["url"] = avatarURL(user)
		j["icon"] = a
		if ban := user.Options.Banner; ban != "" {
			a := junk.New()
			a["type"] = "Image"
			a["mediaType"] = "image/jpg"
			a["url"] = ban
			j["image"] = a
		}
	} else {
		j["type"] = "Service"
	}
	k := junk.New()
	k["id"] = user.URL + "#key"
	k["owner"] = user.URL
	k["publicKeyPem"] = user.Key
	j["publicKey"] = k

	return j
}

var oldjonkers = cache.New(cache.Options{Filler: func(name string) ([]byte, bool) {
	user, err := butwhatabout(name)
	if err != nil {
		return nil, false
	}
	j := junkuser(user)
	return j.ToBytes(), true
}, Duration: 1 * time.Minute})

func asjonker(name string) ([]byte, bool) {
	var j []byte
	ok := oldjonkers.Get(name, &j)
	return j, ok
}

var handfull = cache.New(cache.Options{Filler: func(name string) (string, bool) {
	m := strings.Split(name, "@")
	if len(m) != 2 {
		dlog.Printf("bad fish name: %s", name)
		return "", true
	}
	var href string
	row := stmtGetXonker.QueryRow(name, "fishname")
	err := row.Scan(&href)
	if err == nil {
		return href, true
	}
	dlog.Printf("fishing for %s", name)
	j, err := GetJunkFast(readyLuserOne, fmt.Sprintf("https://%s/.well-known/webfinger?resource=acct:%s", m[1], name))
	if err != nil {
		ilog.Printf("failed to go fish %s: %s", name, err)
		return "", true
	}
	links, _ := j.GetArray("links")
	for _, li := range links {
		l, ok := li.(junk.Junk)
		if !ok {
			continue
		}
		href, _ := l.GetString("href")
		rel, _ := l.GetString("rel")
		t, _ := l.GetString("type")
		if rel == "self" && friendorfoe(t) {
			when := time.Now().UTC().Format(dbtimeformat)
			_, err := stmtSaveXonker.Exec(name, href, "fishname", when)
			if err != nil {
				elog.Printf("error saving fishname: %s", err)
			}
			return href, true
		}
	}
	return href, true
}, Duration: 1 * time.Minute})

func gofish(name string) string {
	if name[0] == '@' {
		name = name[1:]
	}
	var href string
	handfull.Get(name, &href)
	return href
}

func investigate(name string) (*SomeThing, error) {
	if name == "" {
		return nil, fmt.Errorf("no name")
	}
	if name[0] == '@' {
		name = gofish(name)
	}
	if name == "" {
		return nil, fmt.Errorf("no name")
	}
	obj, err := GetJunkFast(readyLuserOne, name)
	if err != nil {
		return nil, err
	}
	allinjest(originate(name), obj)
	return somethingabout(obj)
}

func somethingabout(obj junk.Junk) (*SomeThing, error) {
	info := new(SomeThing)
	t, _ := obj.GetString("type")
	isowned := false
	switch t {
	case "Person":
		fallthrough
	case "Group":
		fallthrough
	case "Organization":
		fallthrough
	case "Application":
		fallthrough
	case "Service":
		info.What = SomeActor
	case "OrderedCollection":
		isowned = true
		fallthrough
	case "Collection":
		info.What = SomeCollection
	default:
		return nil, fmt.Errorf("unknown object type")
	}
	info.XID, _ = obj.GetString("id")
	info.Name, _ = obj.GetString("preferredUsername")
	if info.Name == "" {
		info.Name, _ = obj.GetString("name")
	}
	if isowned {
		info.Owner, _ = obj.GetString("attributedTo")
	}
	if info.Owner == "" {
		info.Owner = info.XID
	}
	return info, nil
}

func allinjest(origin string, obj junk.Junk) {
	keyobj, ok := obj.GetMap("publicKey")
	if ok {
		ingestpubkey(origin, keyobj)
	}
	ingestboxes(origin, obj)
	ingesthandle(origin, obj)
}

func ingestpubkey(origin string, obj junk.Junk) {
	keyobj, ok := obj.GetMap("publicKey")
	if ok {
		obj = keyobj
	}
	keyname, ok := obj.GetString("id")
	var data string
	row := stmtGetXonker.QueryRow(keyname, "pubkey")
	err := row.Scan(&data)
	if err == nil {
		return
	}
	if !ok || origin != originate(keyname) {
		ilog.Printf("bad key origin %s <> %s", origin, keyname)
		return
	}
	dlog.Printf("ingesting a needed pubkey: %s", keyname)
	owner, ok := obj.GetString("owner")
	if !ok {
		ilog.Printf("error finding %s pubkey owner", keyname)
		return
	}
	data, ok = obj.GetString("publicKeyPem")
	if !ok {
		ilog.Printf("error finding %s pubkey", keyname)
		return
	}
	if originate(owner) != origin {
		ilog.Printf("bad key owner: %s <> %s", owner, origin)
		return
	}
	_, _, err = httpsig.DecodeKey(data)
	if err != nil {
		ilog.Printf("error decoding %s pubkey: %s", keyname, err)
		return
	}
	when := time.Now().UTC().Format(dbtimeformat)
	_, err = stmtSaveXonker.Exec(keyname, data, "pubkey", when)
	if err != nil {
		elog.Printf("error saving key: %s", err)
	}
}

func ingestboxes(origin string, obj junk.Junk) {
	ident, _ := obj.GetString("id")
	if ident == "" {
		return
	}
	if originate(ident) != origin {
		return
	}
	var info string
	row := stmtGetXonker.QueryRow(ident, "boxes")
	err := row.Scan(&info)
	if err == nil {
		return
	}
	dlog.Printf("ingesting boxes: %s", ident)
	inbox, _ := obj.GetString("inbox")
	outbox, _ := obj.GetString("outbox")
	sbox, _ := obj.GetString("endpoints", "sharedInbox")
	if inbox != "" {
		when := time.Now().UTC().Format(dbtimeformat)
		m := strings.Join([]string{inbox, outbox, sbox}, " ")
		_, err = stmtSaveXonker.Exec(ident, m, "boxes", when)
		if err != nil {
			elog.Printf("error saving boxes: %s", err)
		}
	}
}

func ingesthandle(origin string, obj junk.Junk) {
	xid, _ := obj.GetString("id")
	if xid == "" {
		return
	}
	if originate(xid) != origin {
		return
	}
	var handle string
	row := stmtGetXonker.QueryRow(xid, "handle")
	err := row.Scan(&handle)
	if err == nil {
		return
	}
	handle, _ = obj.GetString("preferredUsername")
	if handle != "" {
		when := time.Now().UTC().Format(dbtimeformat)
		_, err = stmtSaveXonker.Exec(xid, handle, "handle", when)
		if err != nil {
			elog.Printf("error saving handle: %s", err)
		}
	}
}

func updateMe(username string) {
	var user *WhatAbout
	somenamedusers.Get(username, &user)
	dt := time.Now().UTC().Format(time.RFC3339)
	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = fmt.Sprintf("%s/upme/%s/%d", user.URL, user.Name, time.Now().Unix())
	j["actor"] = user.URL
	j["published"] = dt
	j["to"] = thewholeworld
	j["type"] = "Update"
	j["object"] = junkuser(user)

	msg := j.ToBytes()

	rcpts := make(map[string]bool)
	for _, f := range getdubs(user.ID) {
		if f.XID == user.URL {
			continue
		}
		var box *Box
		boxofboxes.Get(f.XID, &box)
		if box != nil && box.Shared != "" {
			rcpts["%"+box.Shared] = true
		} else {
			rcpts[f.XID] = true
		}
	}
	for a := range rcpts {
		go deliverate(user.ID, a, msg)
	}
}

func followme(user *WhatAbout, who string, name string, j junk.Junk) {
	folxid, _ := j.GetString("id")

	ilog.Printf("updating honker follow: %s %s", who, folxid)

	var x string
	db := opendatabase()
	row := db.QueryRow("select xid from honkers where name = ? and xid = ? and userid = ? and flavor in ('dub', 'undub')", name, who, user.ID)
	err := row.Scan(&x)
	if err != sql.ErrNoRows {
		ilog.Printf("duplicate follow request: %s", who)
		_, err = stmtUpdateFlavor.Exec("dub", folxid, user.ID, name, who, "undub")
		if err != nil {
			elog.Printf("error updating honker: %s", err)
		}
	} else {
		stmtSaveDub.Exec(user.ID, name, who, "dub", folxid)
	}
	go rubadubdub(user, j)
}

func unfollowme(user *WhatAbout, who string, name string, j junk.Junk) {
	var folxid string
	if who == "" {
		folxid, _ = j.GetString("object")

		db := opendatabase()
		row := db.QueryRow("select xid, name from honkers where userid = ? and folxid = ? and flavor in ('dub', 'undub')", user.ID, folxid)
		err := row.Scan(&who, &name)
		if err != nil {
			if err != sql.ErrNoRows {
				elog.Printf("error scanning honker: %s", err)
			}
			return
		}
	}

	ilog.Printf("updating honker undo: %s %s", who, folxid)
	_, err := stmtUpdateFlavor.Exec("undub", folxid, user.ID, name, who, "dub")
	if err != nil {
		elog.Printf("error updating honker: %s", err)
		return
	}
}

func followyou(user *WhatAbout, honkerid int64, sync bool) {
	var url, owner string
	db := opendatabase()
	row := db.QueryRow("select xid, owner from honkers where honkerid = ? and userid = ? and flavor in ('unsub', 'peep', 'presub', 'sub')",
		honkerid, user.ID)
	err := row.Scan(&url, &owner)
	if err != nil {
		elog.Printf("can't get honker xid: %s", err)
		return
	}
	folxid := xfiltrate()
	ilog.Printf("subscribing to %s", url)
	_, err = db.Exec("update honkers set flavor = ?, folxid = ? where honkerid = ?", "presub", folxid, honkerid)
	if err != nil {
		elog.Printf("error updating honker: %s", err)
		return
	}
	if sync {
		subsub(user, url, owner, folxid)
	} else {
		go subsub(user, url, owner, folxid)
	}

}
func unfollowyou(user *WhatAbout, honkerid int64, sync bool) {
	db := opendatabase()
	row := db.QueryRow("select xid, owner, folxid, flavor from honkers where honkerid = ? and userid = ? and flavor in ('unsub', 'peep', 'presub', 'sub')",
		honkerid, user.ID)
	var url, owner, folxid, flavor string
	err := row.Scan(&url, &owner, &folxid, &flavor)
	if err != nil {
		elog.Printf("can't get honker xid: %s", err)
		return
	}
	if flavor == "peep" {
		return
	}
	ilog.Printf("unsubscribing from %s", url)
	_, err = db.Exec("update honkers set flavor = ? where honkerid = ?", "unsub", honkerid)
	if err != nil {
		elog.Printf("error updating honker: %s", err)
		return
	}
	if sync {
		itakeitallback(user, url, owner, folxid)
	} else {
		go itakeitallback(user, url, owner, folxid)
	}
}

func followyou2(user *WhatAbout, j junk.Junk) {
	who, _ := j.GetString("actor")

	ilog.Printf("updating honker accept: %s", who)
	db := opendatabase()
	row := db.QueryRow("select name, folxid from honkers where userid = ? and xid = ? and flavor in ('presub', 'sub')",
		user.ID, who)
	var name, folxid string
	err := row.Scan(&name, &folxid)
	if err != nil {
		elog.Printf("can't get honker name: %s", err)
		return
	}
	_, err = stmtUpdateFlavor.Exec("sub", folxid, user.ID, name, who, "presub")
	if err != nil {
		elog.Printf("error updating honker: %s", err)
		return
	}
}

func nofollowyou2(user *WhatAbout, j junk.Junk) {
	who, _ := j.GetString("actor")

	ilog.Printf("updating honker reject: %s", who)
	db := opendatabase()
	row := db.QueryRow("select name, folxid from honkers where userid = ? and xid = ? and flavor in ('presub', 'sub')",
		user.ID, who)
	var name, folxid string
	err := row.Scan(&name, &folxid)
	if err != nil {
		elog.Printf("can't get honker name: %s", err)
		return
	}
	_, err = stmtUpdateFlavor.Exec("unsub", folxid, user.ID, name, who, "presub")
	_, err = stmtUpdateFlavor.Exec("unsub", folxid, user.ID, name, who, "sub")
	if err != nil {
		elog.Printf("error updating honker: %s", err)
		return
	}
}
