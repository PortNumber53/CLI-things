package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"your_module_path/src/agent"
)

func main() {
	verbose := flag.Bool("verbose", false, "Enable verbose output")
	logfile := flag.String("logfile", "", "Specify a logfile to write logs")
	auto := flag.Bool("auto", false, "Enable automatic mode")

	flag.Parse()

	if *verbose {
		fmt.Println("Verbose mode enabled")
	}

	if *logfile != "" {
		file, err := os.OpenFile(*logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("Failed to open logfile: %v", err)
		}
		defer file.Close()
		log.SetOutput(file)
		log.Println("Logging to file:", *logfile)
	}

	agent.Run(*auto)
}