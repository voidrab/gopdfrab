package pdfrab

type PDFValue interface {
	isPDFValue()
}

type PDFHexString struct{ Value string }
type PDFString struct{ Value string }
type PDFInteger int
type PDFReal float32
type PDFBoolean bool
type PDFName struct{ Value string }
type PDFArray []PDFValue
type PDFDict map[string]PDFValue
type PDFStreamDict PDFDict
type PDFRef struct {
	ObjNum int
	GenNum int
}

func (PDFRef) isPDFValue()        {}
func (PDFBoolean) isPDFValue()    {}
func (PDFInteger) isPDFValue()    {}
func (PDFReal) isPDFValue()       {}
func (PDFString) isPDFValue()     {}
func (PDFHexString) isPDFValue()  {}
func (PDFName) isPDFValue()       {}
func (PDFArray) isPDFValue()      {}
func (PDFDict) isPDFValue()       {}
func (PDFStreamDict) isPDFValue() {}
