package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var tableRegexp = regexp.MustCompile(`^\[.*\]$`)

func main() {
	filename := os.Args[1]

	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Opening file error: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	previousTable := ""
	previousKey := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip empty lines or comment lines
		if line == "" || strings.TrimSpace(line)[0] == '#' {
			continue
		}

		if tableRegexp.MatchString(line) {
			currTable := line[1 : len(line)-1]
			if currTable < previousTable {
				fmt.Printf("File is not sorted. Table [%s] should be before table [%s]\n", currTable, previousTable)
				os.Exit(1)
			}
			previousTable = currTable
			previousKey = "" // reset key when new table is entered
		} else {
			currKey := strings.TrimSpace(strings.Split(line, "=")[0])
			if currKey < previousKey {
				fmt.Printf("File is not sorted. Key %s should be before key %s\n", currKey, previousKey)
				os.Exit(1)
			}
			previousKey = currKey
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Scanning file error: %v\n", err)
		os.Exit(1)
	}
}
