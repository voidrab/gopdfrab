package main

import (
	"fmt"
	"log"

	pdfrab "github.com/voidrab/gopdfrab"
)

func main() {
	path := "test documents/test.pdf"
	doc, err := pdfrab.Open(path)
	if err != nil {
		log.Fatal(err)
	}

	count, err := doc.GetPageCount()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%d page(s) in PDF\n", count)

	metadata, err := doc.GetMetadata()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("PDF metadata:")
	for k, v := range metadata {
		fmt.Printf("%v: %v\n", k, v)
	}

	doc.Close()
}
