package main

import (
	"log"
	"os"
	"sync"
)

type logger struct {
	filename string
	*log.Logger
}

var Logger *logger
var once sync.Once

// start loggeando
func GetInstance() *logger {
	once.Do(func() {
		Logger = createLogger("logger.log")
	})
	return Logger
}

func createLogger(fname string) *logger {
	file, _ := os.OpenFile(fname, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)

	return &logger{
		filename: fname,
		Logger:   log.New(file, "Radius test ", log.Lshortfile),
	}
}

