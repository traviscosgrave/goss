package goss

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aelsabbahy/goss/outputs"
	"github.com/aelsabbahy/goss/system"
	"github.com/aelsabbahy/goss/util"
	"github.com/fatih/color"
	"github.com/patrickmn/go-cache"
	"github.com/urfave/cli"
)

func Serve(c *cli.Context) {
	endpoint := c.String("endpoint")
	color.NoColor = true
	cache := cache.New(c.Duration("cache"), 30*time.Second)

	health := healthHandler{
		c:             c,
		gossConfig:    getGossConfig(c),
		sys:           system.New(c),
		outputer:      getOutputer(c),
		cache:         cache,
		gossMu:        &sync.Mutex{},
		maxConcurrent: c.Int("max-concurrent"),
	}
	if c.String("format") == "json" {
		health.contentType = "application/json"
	}
	http.Handle(endpoint, health)
	listenAddr := c.String("listen-addr")
	log.Printf("Starting to listen on: %s", listenAddr)
	log.Fatal(http.ListenAndServe(c.String("listen-addr"), nil))
}

type res struct {
	exitCode int
	b        bytes.Buffer
}
type healthHandler struct {
	c             *cli.Context
	gossConfig    GossConfig
	sys           *system.System
	outputer      outputs.Outputer
	cache         *cache.Cache
	gossMu        *sync.Mutex
	contentType   string
	maxConcurrent int
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	var resp res

	splitFunc := func(c rune) bool {
		return c == ','
	}
	outputConfig := util.OutputConfig{
		FormatOptions: h.c.StringSlice("format-options"),
	}
	format := r.URL.Query().Get("format")
	if outputs.CheckOutputer(format) {
		h.outputer = getOutputerByName(h.c, format)
	} else {
		h.outputer = getOutputer(h.c)
	}

	rawTags := r.URL.Query().Get("tags")
	tags := strings.FieldsFunc(rawTags, splitFunc)

	log.Printf("%v: requesting health probe for tags %v with format '%v'", r.RemoteAddr, tags, format)

	cacheKey := strings.Join([]string{"res", rawTags, format}, "--")
	tmp, found := h.cache.Get(cacheKey)
	if found {
		resp = tmp.(res)
	} else {
		h.gossMu.Lock()
		defer h.gossMu.Unlock()
		tmp, found := h.cache.Get(cacheKey)
		if found {
			resp = tmp.(res)
		} else {
			h.sys = system.New(h.c)
			log.Printf("%v: Stale cache, running tests", r.RemoteAddr)
			iStartTime := time.Now()
			out := validate(h.sys, h.gossConfig, h.maxConcurrent, tags)
			var b bytes.Buffer
			exitCode := h.outputer.Output(&b, out, iStartTime, outputConfig)
			resp = res{exitCode: exitCode, b: b}
			h.cache.Set(cacheKey, resp, cache.DefaultExpiration)
		}
	}
	if h.contentType != "" {
		w.Header().Set("Content-Type", h.contentType)
	}
	if resp.exitCode == 0 {
		resp.b.WriteTo(w)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		resp.b.WriteTo(w)
	}
}
