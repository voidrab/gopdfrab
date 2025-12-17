package main

import (
	"fmt"
	"log"

	pdfrab "github.com/voidrab/gopdfrab"
)

func main() {
	path := "test documents/pdfa1b_alt.pdf"
	doc, err := pdfrab.Open(path)
	if err != nil {
		log.Fatal(err)
	}

	count, err := doc.GetPageCount()
	if err != nil {
		log.Println(err)
	}

	fmt.Printf("%d page(s) in PDF\n", count)

	metadata, err := doc.GetMetadata()
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println("PDF metadata:")
	for k, v := range metadata {
		fmt.Printf("%v: %v\n", k, v)
	}

	version, err := doc.GetVersion()
	if err != nil {
		log.Println(err)
	}
	fmt.Printf("PDF version is: %v\n", version)

	v, err := doc.Verify(pdfrab.A1_B)
	if err != nil {
		log.Println(err)
	}
	if v.Valid {
		fmt.Println("Document is PDF/A-1b compliant")
	} else {
		fmt.Println("Document is not PDF/A-1b compliant")
		fmt.Println("Issues:")
		for i, v := range v.Issues {
			fmt.Printf("#%v: %v\n", i+1, v)
		}
	}

	doc.Close()
}
