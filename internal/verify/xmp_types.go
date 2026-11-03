package verify

import (
	"maps"
	"regexp"
	"strings"
)

// Namespaces of the schemas XMP predefines.
const (
	nsDC         = "http://purl.org/dc/elements/1.1/"
	nsXMP        = "http://ns.adobe.com/xap/1.0/"
	nsXMPRights  = "http://ns.adobe.com/xap/1.0/rights/"
	nsXMPMM      = "http://ns.adobe.com/xap/1.0/mm/"
	nsXMPBJ      = "http://ns.adobe.com/xap/1.0/bj/"
	nsXMPTPg     = "http://ns.adobe.com/xap/1.0/t/pg/"
	nsXMPDM      = "http://ns.adobe.com/xmp/1.0/DynamicMedia/"
	nsPDF        = "http://ns.adobe.com/pdf/1.3/"
	nsPhotoshop  = "http://ns.adobe.com/photoshop/1.0/"
	nsCameraRaw  = "http://ns.adobe.com/camera-raw-settings/1.0/"
	nsTIFF       = "http://ns.adobe.com/tiff/1.0/"
	nsEXIF       = "http://ns.adobe.com/exif/1.0/"
	nsEXIFAux    = "http://ns.adobe.com/exif/1.0/aux/"
	nsGraphics   = "http://ns.adobe.com/xap/1.0/g/"
	nsImage      = "http://ns.adobe.com/xap/1.0/g/img/"
	nsDimensions = "http://ns.adobe.com/xap/1.0/sType/Dimensions#"
	nsFont       = "http://ns.adobe.com/xap/1.0/sType/Font#"
	nsResEvent   = "http://ns.adobe.com/xap/1.0/sType/ResourceEvent#"
	nsResRef     = "http://ns.adobe.com/xap/1.0/sType/ResourceRef#"
	nsVersion    = "http://ns.adobe.com/xap/1.0/sType/Version#"
	nsJob        = "http://ns.adobe.com/xap/1.0/sType/Job#"
)

// xmpStructType is a structured XMP value type: the namespace of its fields
// and each field's value type.
type xmpStructType struct {
	ns     string
	fields map[string]string
}

// xmpTypeSet holds the XMP value types a schema definition may use. Type
// names are compared in the simplified form xmpSimplifyType returns.
type xmpTypeSet struct {
	// simple maps a simple type to the pattern its value must match; a nil
	// pattern accepts any simple value.
	simple  map[string]*regexp.Regexp
	structs map[string]xmpStructType
}

// fullMatch compiles a pattern that must match a whole value.
func fullMatch(pattern string) *regexp.Regexp {
	return regexp.MustCompile(`^(?:` + pattern + `)$`)
}

// The XMP basic value types other than structures. Text-like types accept
// any simple value.
var xmpBasicSimpleTypes = map[string]*regexp.Regexp{
	"boolean":        fullMatch(`True|False`),
	"integer":        fullMatch(`[+-]?\d+`),
	"real":           fullMatch(`[+-]?\d+\.?\d*|[+-]?\d*\.?\d+`),
	"mimetype":       fullMatch(`[-\w+.]+/[-\w+.]+`),
	"propername":     nil,
	"text":           nil,
	"agentname":      nil,
	"rational":       nil,
	"renditionclass": nil,
	"uri":            nil,
	"url":            nil,
	"xpath":          nil,
	"locale":         nil,
}

// xmpBasicStructTypes are the structured types of every PDF/A part, with the
// fields of closed choice taken by their base type.
var xmpBasicStructTypes = map[string]xmpStructType{
	"dimensions": {nsDimensions, map[string]string{"w": "real", "h": "real", "unit": "text"}},
	"thumbnail": {nsImage, map[string]string{
		"height": "integer", "width": "integer", "image": "text", "format": "text"}},
	"resourceevent": {nsResEvent, map[string]string{
		"action": "text", "instanceID": "uri", "parameters": "text",
		"softwareAgent": "agentname", "when": "date"}},
	"resourceref": {nsResRef, map[string]string{
		"instanceID": "uri", "documentID": "uri", "versionID": "text",
		"renditionClass": "renditionclass", "renditionParams": "text",
		"manager": "agentname", "managerVariant": "text", "manageTo": "uri", "manageUI": "uri"}},
	"version": {nsVersion, map[string]string{
		"comments": "text", "event": "resourceevent", "modifyDate": "date",
		"modifier": "propername", "version": "text"}},
	"job": {nsJob, map[string]string{"name": "text", "id": "text", "url": "url"}},
	"flash": {nsEXIF, map[string]string{
		"Fired": "boolean", "Function": "boolean", "RedEyeMode": "boolean",
		"Return": "text", "Mode": "text"}},
	"oecf/sfr": {nsEXIF, map[string]string{
		"Columns": "integer", "Rows": "integer", "Names": "seq text", "Values": "seq rational"}},
	"cfapattern": {nsEXIF, map[string]string{
		"Columns": "integer", "Rows": "integer", "Values": "seq integer"}},
	"devicesettings": {nsEXIF, map[string]string{
		"Columns": "integer", "Rows": "integer", "Settings": "seq text"}},
}

// xmpPart2StructTypes are the structured types XMP 2005 adds for PDF/A-2.
var xmpPart2StructTypes = map[string]xmpStructType{
	"colorant": {nsGraphics, map[string]string{
		"swatchName": "text", "mode": "text", "type": "text",
		"cyan": "real", "magenta": "real", "yellow": "real", "black": "real",
		"red": "integer", "green": "integer", "blue": "integer",
		"L": "real", "A": "integer", "B": "integer"}},
	"font": {nsFont, map[string]string{
		"fontName": "text", "fontFamily": "text", "fontFace": "text", "fontType": "text",
		"versionString": "text", "composite": "boolean", "fontFileName": "text",
		"childFontFiles": "seq text"}},
	"beatsplicestretch": {nsXMPDM, map[string]string{
		"useFileBeatsMarker": "boolean", "riseInDecibel": "real", "riseInTimeDuration": "time"}},
	"marker": {nsXMPDM, map[string]string{
		"startTime": "any", "duration": "any", "comment": "text", "name": "text",
		"location": "uri", "target": "text", "type": "text"}},
	"media": {nsXMPDM, map[string]string{
		"path": "uri", "track": "text", "startTime": "time", "duration": "time",
		"managed": "boolean", "webStatement": "uri"}},
	"projectlink":     {nsXMPDM, map[string]string{"path": "uri", "type": "text"}},
	"resamplestretch": {nsXMPDM, map[string]string{"quality": "text"}},
	"time":            {nsXMPDM, map[string]string{"value": "integer", "scale": "rational"}},
	"timecode":        {nsXMPDM, map[string]string{"timeValue": "text", "timeFormat": "text"}},
	"timescalestretch": {nsXMPDM, map[string]string{
		"frameSize": "real", "frameOverlappingPercentage": "real", "quality": "text"}},
}

// newXMPPart2TypeSet returns the types of ISO 19005-2 and -3 before any
// extension schema adds its own.
func newXMPPart2TypeSet() *xmpTypeSet {
	ts := &xmpTypeSet{simple: maps.Clone(xmpBasicSimpleTypes), structs: maps.Clone(xmpBasicStructTypes)}
	ts.simple["gpscoordinate"] = fullMatch(`\d{1,3},\d{1,2}(,\d{1,2}|\.\d+)[NSEW]`)
	maps.Copy(ts.structs, xmpPart2StructTypes)
	return ts
}

// clone returns a copy of ts that can be extended without changing ts.
func (ts *xmpTypeSet) clone() *xmpTypeSet {
	return &xmpTypeSet{simple: maps.Clone(ts.simple), structs: maps.Clone(ts.structs)}
}

var xmpChoiceRe = regexp.MustCompile(`(open |closed )?(choice |choice$)(of )?`)

// xmpSimplifyType normalises a value type name: lower case, without "open
// choice of" or "closed choice of", and with Text as the item type of a
// bare Bag, Seq or Alt.
func xmpSimplifyType(t string) string {
	s := strings.TrimSpace(xmpChoiceRe.ReplaceAllString(strings.ToLower(t), ""))
	switch {
	case s == "":
		return "text"
	case strings.HasSuffix(s, "lang alt"):
		return s
	case strings.HasSuffix(s, "bag"), strings.HasSuffix(s, "alt"), strings.HasSuffix(s, "seq"):
		return s + " text"
	}
	return s
}

// arrayItemType splits an array type into its array form and item type.
func arrayItemType(t string) (xmpForm, string, bool) {
	for _, a := range []struct {
		prefix string
		form   xmpForm
	}{{"bag ", xmpBag}, {"alt ", xmpAlt}, {"seq ", xmpSeq}} {
		if rest, ok := strings.CutPrefix(t, a.prefix); ok {
			return a.form, rest, true
		}
	}
	return 0, "", false
}

// known reports whether t names a type of ts.
func (ts *xmpTypeSet) known(t string) bool {
	s := xmpSimplifyType(t)
	for {
		_, item, ok := arrayItemType(s)
		if !ok {
			break
		}
		s = item
	}
	_, simple := ts.simple[s]
	_, structure := ts.structs[s]
	return simple || structure || s == "date" || s == "lang alt"
}

// valid reports whether n is a value of type t.
func (ts *xmpTypeSet) valid(n *xmpNode, t string) bool {
	return ts.validSimplified(n, xmpSimplifyType(t))
}

// validSimplified reports whether n is a value of type s, a name in the form
// xmpSimplifyType returns. Item and field types are in that form too.
func (ts *xmpTypeSet) validSimplified(n *xmpNode, s string) bool {
	if s == "any" {
		return true
	}
	if form, item, ok := arrayItemType(s); ok {
		if n.form != form {
			return false
		}
		for _, c := range n.children {
			if !ts.validSimplified(c, item) {
				return false
			}
		}
		return true
	}
	switch s {
	case "date":
		return n.form == xmpSimple && xmpParseDate(n.value)
	case "lang alt":
		return n.isAltText() || (n.form == xmpAlt && len(n.children) == 0)
	}
	if re, ok := ts.simple[s]; ok {
		return n.form == xmpSimple && (re == nil || re.MatchString(n.value))
	}
	st, ok := ts.structs[s]
	if !ok || n.form != xmpStructure {
		return false
	}
	for _, f := range n.children {
		ft, ok := st.fields[f.name]
		if !ok || f.ns != st.ns || !ts.validSimplified(f, ft) {
			return false
		}
	}
	return true
}

// xmpDateValueRe matches a date XMP readers accept: ISO 8601 with any
// trailing components left out. The pattern does not bound the numeric
// fields, so an out-of-range number is accepted.
var xmpDateValueRe = regexp.MustCompile(
	`^(?:-?\d+(?:-\d+(?:-\d+(?:T\d+(?::\d+(?::\d+(?:\.\d+)?)?)?(?:Z|[+-]\d+(?::\d+)?)?)?)?)?)?$`)

// xmpParseDate reports whether s is a date XMP readers accept.
func xmpParseDate(s string) bool { return xmpDateValueRe.MatchString(s) }

// xmpPart2Properties are the properties of the schemas XMP 2005 predefines,
// by namespace, with each one's value type, as ISO 19005-2 and -3 read them.
var xmpPart2Properties = map[string]map[string]string{
	pdfaIDNamespace: {"part": "integer", "amd": "text", "corr": "text", "conformance": "text"},
	nsDC: {
		"contributor": "bag propername", "coverage": "text", "creator": "seq propername",
		"date": "seq date", "description": "lang alt", "format": "mimetype",
		"identifier": "text", "language": "bag locale", "publisher": "bag propername",
		"relation": "bag text", "rights": "lang alt", "source": "text",
		"subject": "bag text", "title": "lang alt", "type": "bag text",
	},
	nsXMP: {
		"Advisory": "bag text", "BaseURL": "url", "CreateDate": "date",
		"CreatorTool": "agentname", "Identifier": "bag text", "MetadataDate": "date",
		"ModifyDate": "date", "Nickname": "text", "Thumbnails": "alt thumbnail",
		"Label": "text", "Rating": "real",
	},
	nsXMPRights: {
		"Certificate": "url", "Marked": "boolean", "Owner": "bag propername",
		"UsageTerms": "lang alt", "WebStatement": "url",
	},
	nsXMPMM: {
		"DerivedFrom": "resourceref", "DocumentID": "uri", "History": "seq resourceevent",
		"InstanceID": "uri", "ManagedFrom": "resourceref", "Manager": "agentname",
		"ManageTo": "uri", "ManageUI": "uri", "ManagerVariant": "text",
		"RenditionClass": "renditionclass", "RenditionParams": "text", "VersionID": "text",
		"Versions": "seq version", "LastURL": "url", "RenditionOf": "resourceref",
		"SaveID": "integer",
	},
	nsXMPBJ: {"JobRef": "bag job"},
	nsXMPTPg: {
		"MaxPageSize": "dimensions", "NPages": "integer", "Fonts": "bag font",
		"Colorants": "seq colorant", "PlateNames": "seq text",
	},
	nsPDF: {"Keywords": "text", "PDFVersion": "text", "Producer": "agentname"},
	nsPhotoshop: {
		"AuthorsPosition": "text", "CaptionWriter": "propername", "Category": "text",
		"City": "text", "Country": "text", "Credit": "text", "DateCreated": "date",
		"Headline": "text", "Instructions": "text", "Source": "text", "State": "text",
		"TransmissionReference": "text", "Urgency": "integer",
		"SupplementalCategories": "bag text",
	},
	nsTIFF: {
		"ImageWidth": "integer", "ImageLength": "integer", "BitsPerSample": "seq integer",
		"SamplesPerPixel": "integer", "XResolution": "rational", "YResolution": "rational",
		"TransferFunction": "seq integer", "WhitePoint": "seq rational",
		"PrimaryChromaticities": "seq rational", "YCbCrCoefficients": "seq rational",
		"ReferenceBlackWhite": "seq rational", "DateTime": "date",
		"ImageDescription": "lang alt", "Make": "propername", "Model": "propername",
		"Software": "agentname", "Artist": "propername", "Copyright": "lang alt",
		"Compression": "integer", "PhotometricInterpretation": "integer",
		"Orientation": "integer", "PlanarConfiguration": "integer",
		"YCbCrPositioning": "integer", "ResolutionUnit": "integer",
		"YCbCrSubSampling": "seq integer",
	},
	nsEXIF: {
		"CompressedBitsPerPixel": "rational", "PixelXDimension": "integer",
		"PixelYDimension": "integer", "UserComment": "lang alt", "RelatedSoundFile": "text",
		"DateTimeOriginal": "date", "DateTimeDigitized": "date", "ExposureTime": "rational",
		"FNumber": "rational", "SpectralSensitivity": "text", "ISOSpeedRatings": "seq integer",
		"OECF": "oecf/sfr", "ShutterSpeedValue": "rational", "ApertureValue": "rational",
		"BrightnessValue": "rational", "ExposureBiasValue": "rational",
		"MaxApertureValue": "rational", "SubjectDistance": "rational", "Flash": "flash",
		"FocalLength": "rational", "SubjectArea": "seq integer", "FlashEnergy": "rational",
		"SpatialFrequencyResponse": "oecf/sfr", "FocalPlaneXResolution": "rational",
		"FocalPlaneYResolution": "rational", "SubjectLocation": "seq integer",
		"ExposureIndex": "rational", "CFAPattern": "cfapattern", "DigitalZoomRatio": "rational",
		"FocalLengthIn35mmFilm": "integer", "DeviceSettingDescription": "devicesettings",
		"ImageUniqueID": "text", "GPSVersionID": "text", "GPSLatitude": "gpscoordinate",
		"GPSLongitude": "gpscoordinate", "GPSAltitude": "rational", "GPSTimeStamp": "date",
		"GPSSatellites": "text", "GPSDOP": "rational", "GPSSpeed": "rational",
		"GPSTrack": "rational", "GPSImgDirection": "rational", "GPSMapDatum": "text",
		"GPSDestLatitude": "gpscoordinate", "GPSDestLongitude": "gpscoordinate",
		"GPSDestBearing": "rational", "GPSDestDistance": "rational",
		"GPSProcessingMethod": "text", "GPSAreaInformation": "text",
		"ExposureProgram": "integer", "MeteringMode": "integer",
		"FocalPlaneResolutionUnit": "integer", "SensingMethod": "integer",
		"FileSource": "integer", "SceneType": "integer", "CustomRendered": "integer",
		"ExposureMode": "integer", "WhiteBalance": "integer", "SceneCaptureType": "integer",
		"GainControl": "integer", "Contrast": "integer", "Saturation": "integer",
		"Sharpness": "integer", "SubjectDistanceRange": "integer", "GPSAltitudeRef": "integer",
		"GPSStatus": "text", "GPSSpeedRef": "text", "GPSTrackRef": "text",
		"GPSImgDirectionRef": "text", "GPSDestBearingRef": "text",
		"GPSDestDistanceRef": "text", "GPSDifferential": "integer",
		"ComponentsConfiguration": "seq integer",
		"ExifVersion":             "text", "FlashpixVersion": "text", "GPSMeasureMode": "text",
		"ColorSpace": "integer", "LightSource": "integer",
	},
	nsXMPDM: {
		"projectRef": "projectlink", "videoFrameRate": "text", "videoFrameSize": "dimensions",
		"videoPixelAspectRatio": "rational", "videoAlphaUnityIsTransparent": "boolean",
		"videoAlphaPremultipleColor": "colorant", "videoCompressor": "text",
		"audioSampleRate": "integer", "audioCompressor": "text", "speakerPlacement": "text",
		"fileDataRate": "rational", "tapeName": "text", "altTapeName": "text",
		"startTimecode": "timecode", "altTimecode": "timecode", "duration": "time",
		"scene": "text", "shotName": "text", "shotDate": "date", "shotLocation": "text",
		"logComment": "text", "markers": "seq marker", "contributedMedia": "bag media",
		"absPeakAudioFilePath": "uri", "relativePeakAudioFilePath": "uri",
		"videoModDate": "date", "audioModDate": "date", "metadataModDate": "date",
		"artist": "text", "album": "text", "trackNumber": "integer", "genre": "text",
		"copyright": "text", "releaseDate": "date", "composer": "text", "engineer": "text",
		"tempo": "real", "instrument": "text", "introTime": "time", "outCue": "time",
		"relativeTimestamp": "time", "loop": "boolean", "numberOfBeats": "real",
		"timeScaleParams": "timescalestretch", "resampleParams": "resamplestretch",
		"beatSpliceParams": "beatsplicestretch",
		"videoPixelDepth":  "text", "videoColorSpace": "text", "videoAlphaMode": "text",
		"videoFieldOrder": "text", "pullDown": "text", "audioSampleType": "text",
		"audioChannelType": "text", "key": "text", "stretchMode": "text",
		"timeSignature": "text", "scaleType": "text",
	},
	nsCameraRaw: {
		"AutoBrightness": "boolean", "AutoContrast": "boolean", "AutoExposure": "boolean",
		"AutoShadows": "boolean", "BlueHue": "integer", "BlueSaturation": "integer",
		"Brightness": "integer", "CameraProfile": "text", "ChromaticAberrationB": "integer",
		"ChromaticAberrationR": "integer", "ColorNoiseReduction": "integer",
		"Contrast": "integer", "CropTop": "real", "CropLeft": "real", "CropBottom": "real",
		"CropRight": "real", "CropAngle": "real", "CropWidth": "real", "CropHeight": "real",
		"CropUnits": "integer", "Exposure": "real", "GreenHue": "integer",
		"GreenSaturation": "integer", "HasCrop": "boolean", "HasSettings": "boolean",
		"LuminanceSmoothing": "integer", "RawFileName": "text", "RedHue": "integer",
		"RedSaturation": "integer", "Saturation": "integer", "Shadows": "integer",
		"ShadowTint": "integer", "Sharpness": "integer", "Temperature": "integer",
		"Tint": "integer", "ToneCurveName": "text", "Version": "text",
		"VignetteAmount": "integer", "VignetteMidpoint": "integer",
		"WhiteBalance": "text", "ToneCurve": "seq text",
	},
	nsEXIFAux: {"Lens": "text", "SerialNumber": "text"},
}

// xmpPart2Types are the value types the predefined schemas of ISO 19005-2
// and -3 use.
var xmpPart2Types = newXMPPart2TypeSet()
