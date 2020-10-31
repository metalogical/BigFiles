package main

import (
	"log"
	"net/http"
	"os"

	"github.com/akrylysov/algnhsa"
	"github.com/metalogical/BigFiles/auth"
	"github.com/metalogical/BigFiles/server"
)

func main() {
	user := os.Getenv("LFS_USER")
	pass := os.Getenv("LFS_PASS")
	if user == "" || pass == "" {
		log.Fatalln("LFS_USER and LFS_PASS must be set")
	}
	bucket := os.Getenv("LFS_BUCKET")
	if bucket == "" {
		log.Fatalln("LFS_BUCKET must be set")
	}

	s, err := server.New(server.Options{
		Bucket:       bucket,
		IsAuthorized: auth.Static(user, pass),
	})
	if err != nil {
		log.Fatalln(err)
	}

	log.Println("serving on http://localhost:5000")
	if err := http.ListenAndServe("127.0.0.1:5000", s); err != nil {
		log.Fatalln(err)
	}

	// or try running the server on AWS Lambda
	algnhsa.ListenAndServe(s, nil)
}
