package main

import (
	"github.com/pkg/errors"
	"github.com/shchiv/notifier-service/pkg/chaos"
	"net/http"
)

var cfg chaos.Config

func init() {
	cfg = chaos.ParseConfig()
}

func chaosMonkey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := chaos.Start(cfg); err != nil {
			if errors.Is(err, chaos.NightmareException) {
				w.Header().Set("mode", chaos.Nightmare)
			}
			if errors.Is(err, chaos.HellException) {
				w.Header().Set("mode", chaos.Hell)
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	}
}
