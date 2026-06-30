# PDF/A-1 validation rules

## Attribution

Content taken from [veraPDF-validation-profiles](https://github.com/veraPDF/veraPDF-validation-profiles), licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)

## Rule 6.1.2-1

### Requirement

>*The % character of the file header shall occur at byte offset 0 of the file. The first line of a PDF file is a header identifying the version of the PDF specification to which the file conforms.*

### Error details

File header starts at non-zero offset or does not match the pattern %PDF-n.m.

* Object type: `CosDocument`
* Test condition: `headerOffset == 0 && /%PDF-\d\.\d/.test(header)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 3.4.1

## Rule 6.1.2-2

### Requirement

>*The file header line shall be immediately followed by a comment consisting of a % character followed by at least four characters, each of whose encoded byte values shall have a decimal value greater than 127.*

### Error details

Binary comment in the file header is missing or does not start with 4 bytes with byte values above 127.

The presence of encoded character byte values greater than decimal 127 near the beginning of a file is used by various software tools and protocols to classify the file as containing 8-bit binary data that should be preserved during processing.

* Object type: `CosDocument`
* Test condition: `headerByte1 > 127 && headerByte2 > 127 && headerByte3 > 127 && headerByte4 > 127`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.3-1

### Requirement

>*The file trailer dictionary shall contain the ID keyword. The file trailer referred to is either the last trailer dictionary in a PDF file, as described in PDF Reference 3.4.4 and 3.4.5, or the first page trailer in a linearized PDF file, as described in PDF Reference F.2.*

### Error details

Missing ID keyword in the document trailer.

Processing systems and documents may contain references to PDF files. Simply storing a file name, however, even in a platform-independent format, does not guarantee that the file can be found. Even if the file still exists and its name has not been changed, different server software applications may identify it in different ways. For example, servers running on DOS platforms must convert all file names to 8 characters and a 3-character extension; different servers may use different strategies for converting longer file names to this format.

External file references can be made more reliable by including a file identifier in the file itself and using it in addition to the normal platform-based file designation. File identifiers are defined by the optional ID entry in a PDF file’s trailer dictionary. The value of this entry is an array of two strings. The first string is a permanent identifier based on the contents of the file at the time it was originally created, and does not change when the file is incrementally updated. The second string is a changing identifier based on the file's contents at the time it was last updated. When a file is first written, both identifiers are set to the same value. If both identifiers match when a file reference is resolved, it is very likely that the correct file has been found; if only the first identifier matches, then a different version of the correct file has been found.

* Object type: `CosDocument`
* Test condition: `(isLinearized == true) ? (firstPageID != null) : (lastID != null)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 3.3.4, 3.3.5, 9.3, F.2

## Rule 6.1.3-2

### Requirement

>*The keyword Encrypt shall not be used in the trailer dictionary.*

### Error details

Encrypt keyword is present in the trailer dictionary. 

The explicit prohibition of the Encrypt keyword has the implicit effect of disallowing encryption and password-protected
access permissions.

* Object type: `CosTrailer`
* Test condition: `isEncrypted != true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.3-3

### Requirement

>*No data shall follow the last end-of-file marker except a single optional end-of-line marker.*

### Error details

Data is present after the last end-of-file marker.

The trailer of a PDF file enables an application reading the file to quickly find the cross-reference table and certain special objects. Applications should read a PDF file from its end. The last line of the file contains only the end-of-file marker, %%EOF. Some PDF viewers require only that the %%EOF marker appear somewhere within the last 1024 bytes of the file. But having any data after %%EOF marker introduces risks that the PDF document might not be processed correctly. 

* Object type: `CosDocument`
* Test condition: `postEOFDataSize == 0`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.3-4

### Requirement

>*In a linearized PDF, if the ID keyword is present in both the first page trailer dictionary and the last trailer dictionary, the value to both instances of the ID keyword shall be identical.*

### Error details

The Linearized PDF contains both the ID keyword in the last trailer and in the first page trailer, and these IDs do not match.

A linearized PDF file is one that has been organized in a special way to enable efficient incremental access in a network environment. The file is valid PDF in all respects, and is compatible with all existing viewers and other PDF applications. Enhanced viewer applications can recognize that a PDF file has been linearized and can take advantage of that organization (as well as added "hint" information) to enhance viewing performance.

As a result of this optimization, the ID of the linearized PDF can be fount in two different places: in the last document trailer and in the trailer for the first page, normally located at the beginning of the document. These two IDs shall be identical to avoid any ambiguities in identifying the PDF document by its ID entry. 

* Object type: `CosDocument`
* Test condition: `(isLinearized != true) || lastID == null || (firstPageID == lastID)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO 19005-1:2005/Cor.2:2011, 6.1.3
  * Rule 6.1.3-1

## Rule 6.1.4-1

### Requirement

>*In a cross reference subsection header the starting object number and the range shall be separated by a single SPACE character (20h).*

### Error details

Extra or missing spacings around in a subsection header of the cross reference table.

The cross-reference table contains information that permits random access to indirect objects within the file, so that the entire file need not be read to locate any particular object. The table contains a one-line entry for each indirect object, specifying the location of that object within the body of the file.

The cross-reference table is the only part of a PDF file with a fixed format; this permits entries in the table to be accessed randomly. Any variations in this format, including unnecessary SPACE characters may result in incorrect parsing of the cross-reference table and, thus, errors in reading the PDF document. 

* Object type: `CosXRef`
* Test condition: `subsectionHeaderSpaceSeparated == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.4-2

### Requirement

>*The xref keyword and the cross reference subsection header shall be separated by a single EOL marker.*

### Error details

Extra spacings or missing EOL characters after the 'xref' keyword in the cross reference table.

As mentioned in Rule 6.1.4-1, any variations in cross reference table format may result in incorrect parsing of the cross-reference table and, thus, errors in reading the PDF document.

* Object type: `CosXRef`
* Test condition: `xrefEOLMarkersComplyPDFA`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * Rule 6.1.4-1

## Rule 6.1.4-3

### Requirement

>*Xref streams shall not be used.*

### Error details

The document uses xref streams.

* Object type: `PDDocument`
* Test condition: `containsXRefStream == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.6-1

### Requirement

>*Hexadecimal strings shall contain an even number of non-white-space characters.*

### Error details

A hexadecimal string contains odd number of non-white-space characters.

Strings in PDF documents may be written as a literal byte sequence or as a hexadecimal string; the latter is useful for including arbitrary binary data in a PDF file. A hexadecimal string is written as a sequence of hexadecimal digits (0–9 and either A–F or a–f) enclosed within angle brackets (< and >):

`<4E6F762073686D6F7A206B6120706F702E>`

Each pair of hexadecimal digits defines one byte of the string. White-space characters (such as space, tab, carriage return, line feed, and form feed) are ignored.

### PDF Validation Technical Working Group notes

White-space characters are defined as NULL (00h), TAB (09h), LINE FEED (0Ah), FORM FEED (0Ch), CARRIAGE RETURN (0Dh), SPACE (20h). They may appear within hexadecimal strings for formatting purposes:

`<4E6F 7620 7368 6D6F` 
` 7A20 6B61 2070 6F70>`

* Object type: `CosString`
* Test condition: `(isHex != true) || hexCount % 2 == 0`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.6-2

### Requirement

>*All non-white-space characters in hexadecimal strings shall be in the range 0 to 9, A to F or a to f.*

### Error details

Hexadecimal string contains non-white-space characters outside the range 0 to 9, A to F or a to f.

* Object type: `CosString`
* Test condition: `(isHex != true) || containsOnlyHex == true`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Other references:
  * Rule 6.1.6-1

## Rule 6.1.7-1

### Requirement

>*The value of the Length key specified in the stream dictionary shall match the number of bytes in the file following the LINE FEED character after the stream keyword and preceding the EOL marker before the endstream keyword.*

### Error details

Actual length of the stream does not match the value of the Length key in the Stream dictionary. 

Every stream dictionary has a Length entry that indicates how many bytes of the PDF file are used for the stream's data. (If the stream has a filter, Length is the number of bytes of encoded data.) In addition, most filters are defined so that the data is self-limiting; that is, they use an encoding scheme in which an explicit end-of-data (EOD) marker delimits the extent of the data. Finally, streams are used to represent many objects from whose attributes a length can be inferred. All of these constraints must be consistent.

* Object type: `CosStream`
* Test condition: `Length == realLength`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.7-2

### Requirement

>*The stream keyword shall be followed either by a CARRIAGE RETURN (0Dh) and LINE FEED (0Ah) character sequence or by a single LINE FEED character. The endstream keyword shall be preceded by an EOL marker.*

### Error details

Extra spacings or missing EOL characters around keywords 'stream' and 'endstream'.

These requirements remove potential ambiguity regarding the ending of stream content.

* Object type: `CosStream`
* Test condition: `streamKeywordCRLFCompliant == true && endstreamKeywordEOLCompliant == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.7-3

### Requirement

>*A stream object dictionary shall not contain the F, FFilter, or FDecodeParms keys.*

### Error details

A stream object dictionary contains one of the F, FFilter, or FDecodeParms keys.

These keys are used to point to document content external to the file. The explicit prohibition of these keys has the implicit effect of disallowing external content that can create external dependencies and complicate preservation efforts.

* Object type: `CosStream`
* Test condition: `F == null && FFilter == null && FDecodeParms == null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * 19005-1:2005/Cor.2:2011, 6.1.7

## Rule 6.1.8-1

### Requirement

>*The object number and generation number shall be separated by a single white-space character. The generation number and obj keyword shall be separated by a single white-space character. The object number and endobj keyword shall each be preceded by an EOL marker. The obj and endobj keywords shall each be followed by an EOL marker.*

### Error details

Extra spacings or missing EOL characters around indirect object/generation number or keywords 'obj' and 'endobj'.

The definition of an indirect object in a PDF file consists of its object number and generation number, followed by the value of the object itself bracketed between the keywords "obj" and "endobj". The requirements of this rule guarantee that the definition of an indirect object can be parsed unambiguously. 

* Object type: `CosIndirect`
* Test condition: `spacingCompliesPDFA`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.10-1

### Requirement

>*The LZWDecode filter shall not be permitted.*

### Error details

LZW compression is used.

The use of the LZW compression algorithm has been subject to intellectual property constraints.

* Object type: `CosFilter`
* Test condition: `internalRepresentation != "LZWDecode"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.1.10-2

### Requirement

>*The LZWDecode filter shall not be permitted.*

### Error details

LZW compression is used in the inline image.

Inline images are defined directly within the content stream in which it will be painted, rather than as separate objects. The use of LZW compression is also not permitted for such images.

* Object type: `CosIIFilter`
* Test condition: `internalRepresentation != "LZWDecode" && internalRepresentation != "LZW"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Other references:
  * Rule 6.1.10-1

## Rule 6.1.11-1

### Requirement

>*A file specification dictionary, as defined in PDF 3.10.2, shall not contain the EF key.*

### Error details

A file specification dictionary contains the EF key. 

This key is used to encapsulate files containing arbitrary content within a PDF file. The explicit prohibition of EF key has the implicit effect of disallowing embedded files that can create external dependencies and complicate preservation efforts.

* Object type: `CosFileSpecification`
* Test condition: `containsEF == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 3.10.2

## Rule 6.1.11-2

### Requirement

>*A file's name dictionary, as defined in PDF Reference 3.6.3, shall not contain the EmbeddedFiles key.*

### Error details

The document contains embedded files (EmbeddedFiles key is present in the file's name dictionary).

PDF/A-1 standard does not permit embedded files. This requirement was relaxed in PDF/A-2 to embed other PDF documents conforming to either PDF/A-1 or PDF/A-2, and in PDF/A-3 to allow any embedded files.

* Object type: `CosDocument`
* Test condition: `containsEmbeddedFiles == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 3.6.3

## Rule 6.1.12-1

### Requirement

>*Largest Integer value is 2,147,483,647. Smallest integer value is -2,147,483,648.*

### Error details

Integer value is out of range.

* Object type: `CosInteger`
* Test condition: `(intValue <= 2147483647) && (intValue >= -2147483648)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-2

### Requirement

>*Absolute real value must be less than or equal to 32767.0.*

### Error details

Real value is out of range.

* Object type: `CosReal`
* Test condition: `(realValue >= -32767.0) && (realValue <= 32767.0)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-3

### Requirement

>*Maximum length of a string (in bytes) is 65535.*

### Error details

Maximum length of a String (65535) is exceeded.

* Object type: `CosString`
* Test condition: `value.length() < 65536`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-4

### Requirement

>*Maximum length of a name (in bytes) is 127.*

### Error details

Maximum length of a Name (127 bytes) is exceeded.

* Object type: `CosName`
* Test condition: `internalRepresentation.length() <= 127`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-5

### Requirement

>*Maximum capacity of an array (in elements) is 8191.*

### Error details

Maximum capacity of an array (8191) is exceeded.

* Object type: `CosArray`
* Test condition: `size <= 8191`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-6

### Requirement

>*Maximum capacity of a dictionary (in entries) is 4095.*

### Error details

Maximum capacity of a dictionary (4095) is exceeded.

* Object type: `CosDict`
* Test condition: `size <= 4095`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-7

### Requirement

>*Maximum number of indirect objects in a PDF file is 8,388,607.*

### Error details

Maximum number of indirect objects (8,388,607) in a PDF file is exceeded.

* Object type: `CosDocument`
* Test condition: `nrIndirects <= 8388607`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-8

### Requirement

>*Maximum depth of graphics state nesting by q and Q operators is 28.*

### Error details

Maximum depth of graphics state nesting (q and Q operators) is exceeded.

* Object type: `Op_q_gsave`
* Test condition: `nestingLevel <= 28`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-9

### Requirement

>*Maximum number of DeviceN components is 8.*

### Error details

Maximum number of DeviceN components (8) is exceeded.

* Object type: `PDDeviceN`
* Test condition: `nrComponents <= 8`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.12-10

### Requirement

>*Maximum value of a CID (character identifier) is 65,535.*

### Error details

Maximum value of a CID (65,535) is exceeded.

* Object type: `CMapFile`
* Test condition: `maximalCID <= 65535`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF Reference 1.4, Table C.1

## Rule 6.1.13-1

### Requirement

>*The document catalog dictionary shall not contain a key with the name OCProperties.*

### Error details

The document catalog dictionary contains the OCProperties entry.

The explicit prohibition of the OCProperties key has the implicit effect of disallowing optional content that generates alternative renderings of a document.

* Object type: `CosDocument`
* Test condition: `isOptionalContentPresent == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.2.2-1

### Requirement

>*A PDF/A-1 OutputIntent is an OutputIntent dictionary, as defined by PDF Reference 9.10.4, that is included in the file's OutputIntents array and has GTS_PDFA1 as the value of its S key and a valid ICC profile stream as the value its DestOutputProfile key.*

### Error details

The embedded PDF/A Output Intent colour profile is either invalid or does not provide BToA information.

PDF/A standard requires that all colours shall be specified in a device-independent manner, either directly by the use of a device-independent colour space, or indirectly by the use of an OutputIntent.

The BToA information in ICC profiles connects the PDF colour space with the colour space of the output device. 

The ICC specification is an evolving standard. The ICCBased color spaces supported in PDF 1.3 are based on ICC specification version 3.3; those in PDF 1.4 are based on the ICC specification ICC.1:1998-09 and its addendum ICC.1A:1999-04. 

### PDF Validation Technical Working Group notes

It is generally accepted in practice that PDF/A-1 documents may use any ICC colour profiles with their internal version number "major.minor", where "major" number is 2 or less. Such output profiles are fully backward compatible with the ICC specification ICC.1A:1999-04, which has internal version number "2.3".

* Object type: `ICCOutputProfile`
* Test condition: `(deviceClass == "prtr" || deviceClass == "mntr") && (colorSpace == "RGB " || colorSpace == "CMYK" || colorSpace == "GRAY") && version < 3.0`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 9.10.4
  * International Color Consortium, [ICC Specifications](http://color.org/icc_specs2.xalter)

## Rule 6.2.2-2

### Requirement

>*If a file's OutputIntents array contains more than one entry, then all entries that contain a DestOutputProfile key shall have as the value of that key the same indirect object, which shall be a valid ICC profile stream.*

### Error details

File's OutputIntents array contains several output intent dictionaries with non-matching destination output profiles.

A PDF document may conform to several PDF standards at the same time, such as PDF/X, PDF/E or PDF/UA. Each of these standards relies on the presence of the OutputIntent color profile and has to identify such profile via a standard-specific subtype entry (S key in the OutputIntent dictionary). For example, the value of this key is "GTS_PDFA1" for the PDF/A standards, and is "GTS_PDFX" for PDF/X standards. 

The requirement for all output intent dictionaries to share the same output ICC profile minimizes risks that a wrong ICC output profile is used for rendering the PDF document.

* Object type: `OutputIntents`
* Test condition: `sameOutputProfileIndirect == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.2.3.2-1

### Requirement

>*All ICCBased colour spaces shall be embedded as ICC profile streams as described in PDF Reference 4.5*

### Error details

The embedded ICC profile is either invalid or does not satisfy PDF 1.4 requirements. 

ICC profiles can be used in PDF documents to identify the source color spaces. Similar to the requirements for the ICC output profile (see Rule 6.2.2-1), the embedded ICC input profile shall satisfy a number of additional requirements of PDF 1.4 Specification. These requirements cover the device class of the ICC profile, its connection color space and the version of the ICC standard the input profile is based on.

* Object type: `ICCInputProfile`
* Test condition: `(deviceClass == "prtr" || deviceClass == "mntr" || deviceClass == "scnr" || deviceClass == "spac") && 
			(colorSpace == "RGB " || colorSpace == "CMYK" || colorSpace == "GRAY" || colorSpace == "Lab ") && version < 3.0`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 4.5.4, ICCBased Color Spaces
  * International Color Consortium, [ICC Specifications](http://color.org/icc_specs2.xalter)
  * Rule 6.2.2-1

## Rule 6.2.3.2-2

### Requirement

>*All ICCBased colour spaces shall be embedded as ICC profile streams as described in PDF Reference 4.5. The number of color components in the color space described by the ICC profile data must match the number of components actually in the ICC profile. As of PDF 1.4, N must be 1, 3, or 4.*

### Error details

The N entry in the ICC profile dictionary is missing or does not match the number of components in the embedded ICC profile.

PDF 1.4 References requires that the number of color components in the color space described by the ICC profile data must match the number of components actually in the ICC profile. As of PDF 1.4, N must be 1, 3, or 4.

A conforming reader shall render ICCBased colour spaces as specified by the ICC specification, and shall not use the Alternate colour space specified in an ICC profile stream dictionary.

* Object type: `ICCProfile`
* Test condition: `N != null && ((N == 1 && colorSpace == "GRAY") || (N == 3 && (colorSpace == "RGB " || colorSpace == "Lab ")) || (N == 4 && colorSpace == "CMYK"))`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.5.4, Table 4.16

## Rule 6.2.3.3-1

### Requirement

>*DeviceRGB may be used only if the file has a PDF/A-1 OutputIntent that uses an RGB colour space.*

### Error details

DeviceRGB colour space is used without RGB-based output intent ICC profile.

A conforming PDF/A document may use either DeviceRGB or DeviceCMYK colour spaces, but shall not use both. In an uncalibrated colour space is used in a file then that file shall contain an PDF/A-1 OutputIntent specifying the output ICC profile with matching colour space.

When rendering colours specified in a device-dependent colour space a conforming reader shall use file's PDF/A-1 OutputIntent dictionary as the source colour space. This guarantees unambiguous and device-independent representation of all colours used in the conforming PDF/A document.

* Object type: `PDDeviceRGB`
* Test condition: `gOutputCS != null && gOutputCS == "RGB "`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.2.3.3-2

### Requirement

>*DeviceCMYK may be used only if the file has a PDF/A-1 OutputIntent that uses an CMYK colour space.*

### Error details

DeviceCMYK colour space is used without RGB-based output intent ICC profile.

A conforming PDF/A document may use either DeviceRGB or DeviceCMYK colour spaces, but shall not use both. In an uncalibrated colour space is used in a file then that file shall contain an PDF/A-1 OutputIntent specifying the output ICC profile with matching colour space.

When rendering colours specified in a device-dependent colour space a conforming reader shall use file's PDF/A-1 OutputIntent dictionary as the source colour space. This guarantees unambiguous and device-independent representation of all colours used in the conforming PDF/A document.

* Object type: `PDDeviceCMYK`
* Test condition: `gOutputCS != null && gOutputCS == "CMYK"`
* Specification: ISO 19005-1:2005
* Levels: A, B


## Rule 6.2.3.3-3

### Requirement

>*If an uncalibrated colour space is used in a file then that file shall contain a PDF/A-1 OutputIntent, as defined in 6.2.2.*

### Error details

DeviceGray colour space is used without output intent ICC profile.

When rendering a DeviceGray colour specification in a file whose OutputIntent is an RGB profile, a conforming reader shall convert the DeviceGray colour specification to RGB by the method described in PDF 1.4 Reference 6.2.1:

<pre>
red = gray
green = gray
blue = gray
</pre>

When rendering a DeviceGray colour specification in a file whose OutputIntent is a CMYK profile, a conforming reader shall convert the DeviceGray colour specification to DeviceCMYK by the method described in PDF 1.4 Reference 6.2.2:

<pre>
cyan = 0.0
magenta = 0.0
yellow = 0.0
black = 1.0 – gray
</pre>

* Object type: `PDDeviceGray`
* Test condition: `gOutputCS != null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 6.2.1, 6.2.2

## Rule 6.2.4-1

### Requirement

>*An Image dictionary shall not contain the Alternates key.*

### Error details

Alternates key is present in the Image dictionary.

Alternate images provide a straightforward and backward-compatible way to include multiple versions of an image in a PDF file for different purposes. These variant representations of the image may differ, for example, in resolution or in color space. The primary goal is to reduce the need to maintain separate versions of a PDF document for low-resolution on-screen viewing and highresolution printing. However, this mechanism is prohibited in PDF/A-compliant documents, as it introduces risks of choosing different images for rendering.

* Object type: `PDXImage`
* Test condition: `containsAlternates == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.8.4, Alternate Images

## Rule 6.2.4-2

### Requirement

>*An XObject dictionary (Image or Form) shall not contain the OPI key.*

### Error details

OPI key is present in an XObject dictionary.

The Open Prepress Interface (OPI) is a mechanism, originally developed by Aldus Corporation, for creating low-resolution placeholders, or proxies, for such high-resolution images. The proxy typically consists of a downsampled version of the full-resolution image, to be used for screen display and proofing. Before the document is printed, it passes through a filter known as an OPI server, which replaces the proxies with the original full-resolution images.
Similar to Rule 6.2.4-1, this mechanism is prohibited in PDF/A-compliant documents, as it introduces risks of unpredictable PDF rendering.

* Object type: `PDXObject`
* Test condition: `containsOPI == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 9.10.6, Open Prepress Interface (OPI)
  * Rule 6.2.4-1

## Rule 6.2.4-3

### Requirement

>*If an Image dictionary contains the Interpolate key, its value shall be false.*

### Error details

The value of the Interpolate key in the Image dictionary is true.

When the resolution of a source image is significantly lower than that of the output device, each source sample covers many device pixels. This can cause images to appear "jaggy" or "blocky." These visual artifacts can be reduced by applying an image interpolation algorithm during rendering. Instead of painting all pixels covered by a source sample with the same color, image interpolation attempts to produce a smooth transition between adjacent sample values. 

However, the interpolation algorithm is implementation-dependent and is not specified by PDF. Image interpolation may not always be performed for some classes of images or on some output devices. Therefore, this mechanism is not permitted in PDF/A-compliant documents.

* Object type: `PDXImage`
* Test condition: `Interpolate == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.8.4, Image interpolation

## Rule 6.2.4-4

### Requirement

>*If an Image dictionary contains the BitsPerComponent key, its value shall be 1, 2, 4 or 8*

### Error details

The value of the BitsPerComponent key in the Image dictionary is not equal to 1, 2, 4 or 8

* Object type: `PDXImage`
* Test condition: `isMask == true || BitsPerComponent == null || BitsPerComponent == 1 || BitsPerComponent == 2 || BitsPerComponent == 4 || BitsPerComponent == 8`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.8.4, Table 4.35

## Rule 6.2.4-5

### Requirement

>*If an image mask dictionary contains the BitsPerComponent key, its value shall be 1*

### Error details

The value of the BitsPerComponent key in the image mask dictionary is not equal to 1

* Object type: `PDMaskImage`
* Test condition: `BitsPerComponent == null || BitsPerComponent == 1`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.8.4, Table 4.35

## Rule 6.2.5-1

### Requirement

>*A form XObject dictionary shall not contain the Subtype2 key with a value of PS or the PS key.*

### Error details

The form XObject dictionary contains a PS key or Subtype2 key with value PS.

In earlier versions of PDF, a content stream can include PostScript language fragments. These fragments are used only when printing to a PostScript output device; they have no effect either when viewing the document on-screen or when printing to a non-PostScript device. In addition, applications that understand PDF are unlikely to be able to interpret the PostScript fragments. Hence, this capability should be used with extreme caution and only if there is no other way to achieve the same result. Inappropriate use of PostScript XObjects can cause PDF files to print incorrectly.

* Object type: `PDXForm`
* Test condition: `(Subtype2 == null || Subtype2 != "PS") && containsPS == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.10, PostScript XObjects

## Rule 6.2.6-1

### Requirement

>*A conforming file shall not contain any reference XObjects.*

### Error details

The document contains a reference XObject (Ref key in the form XObject dictionary).

Reference XObjects enable one PDF document to import content from another. The document in which the reference occurs is called the containing document; the one whose content is being imported is the target document. The target document may reside in a file external to the containing document or may be included within it as an embedded file stream. 

As this makes the initial PDF document dependent on the presence of external resources, this mechanism is not permitted in PDF/A-compliant documents.

* Object type: `PDXForm`
* Test condition: `containsRef == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.9.3, Reference XObjects

## Rule 6.2.7-1

### Requirement

>*A conforming file shall not contain any PostScript XObjects.*

### Error details

The document contains a PostScript XObject.

PostScript XObjects contain arbitrary executable PostScript code streams that have the potential to interfere with reliable and predictable rendering. See Rule 6.2.5-1 for more detail.

* Object type: `PDXObject`
* Test condition: `Subtype != "PS"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.10, PostScript XObjects
  * Rule 6.2.5-1

## Rule 6.2.8-1

### Requirement

>*An ExtGState dictionary shall not contain the TR key.*

### Error details

An ExtGState dictionary contains the TR key.

In PDF, a transfer function adjusts the values of color components to compensate for nonlinear response in an output device and in the human eye. Each component of a device color space (for example, the red component of the DeviceRGB space) is intended to represent the perceived lightness or intensity of that color component in proportion to the component’s numeric value. Many devices do not actually behave this way, however; the purpose of a transfer function is to compensate for the device's actual behavior. 

As this may lead in significantly different visual appearance of PDF documents on different devices, the use of transfer functions is not permitted by PDF/A.

* Object type: `PDExtGState`
* Test condition: `containsTR == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 6.3, Transfer Functions

## Rule 6.2.8-2

### Requirement

>*An ExtGState dictionary shall not contain the TR2 key with a value other than Default.*

### Error details

An ExtGState dictionary contains the TR2 key with a value other than Default.

The TR2 key has the same meaning as TR key (see Rule 6.2.8-1) except that the value may also be the name or name Default, denoting the transfer function used by default by the output device. And this is the only case, permitted in PDF/A-compliant documents.

* Object type: `PDExtGState`
* Test condition: `containsTR2 == false || TR2NameValue == "Default"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * Rule 6.2.8-1

## Rule 6.2.9-1

### Requirement

>*Where a rendering intent is specified, its value shall be one of the four values defined in PDF Reference RelativeColorimetric, AbsoluteColorimetric, Perceptual or Saturation.*

### Error details

A rendering intent with non-standard value is used.

Although CIE-based color specifications are theoretically device-independent, they are subject to practical limitations in the color reproduction capabilities of the output device. Such limitations may sometimes require compromises to be made among various properties of a color specification when rendering colors for a given device. Specifying a rendering intent allows a PDF file to set priorities regarding which of these properties to preserve and which to sacrifice.

PDF 1.4 defines four standard rendering intents, which can be specified directly in the page content stream via the "ri" operator or attached to any image as an extra property. These standard intents have been deliberately chosen to correspond closely to those defined by the International Color Consortium (ICC), an industry organization that has developed standards for device-independent color. 

However, that the exact set of rendering intents supported may vary from one output device to another; a particular device may not support all possible intents, or may support additional ones beyond those listed in the table. To guarantee the predictability of PDF rendering results, non-standard rendering intents are not permitted in PDF/A-compliant documents.

* Object type: `CosRenderingIntent`
* Test condition: `internalRepresentation == "RelativeColorimetric" || internalRepresentation == "AbsoluteColorimetric" || internalRepresentation == "Perceptual" || internalRepresentation == "Saturation"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 4.5.4, Rendering Intents

## Rule 6.2.10-1

### Requirement

>*A content stream shall not contain any operators not defined in PDF Reference even if such operators are bracketed by the BX/EX compatibility operators.*

### Error details

A content stream contains an operator not defined in PDF Reference.

Ordinarily, when a viewer application encounters an operator in a content stream that it does not recognize, an error will occur. A pair of compatibility operators, BX and EX, modify this behavior. These operators must occur in pairs and may be nested. They bracket a compatibility section, a portion of a content stream within which unrecognized operators are to be ignored without error. This mechanism enables a PDF document to use operators defined in newer versions of PDF without sacrificing compatibility with older viewers.

However, as the use of undefined operators may still result in error for some PDF processors, their use is not permitted in PDF/A-compliant documents, even if they are bracketed by BX/EX compatibility operators.

In earlier versions of the PDF format a PostScript operator "PS" was defined. As this operator is not defined in PDF Reference its use is implicitly prohibited by PDF/A-1 specification.

* Object type: `Op_Undefined`
* Test condition: `false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Specification, 3.7.1, Content Streams

## Rule 6.3.2-1

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5.*

>*Type - name - (Required) The type of PDF object that this dictionary describes; must be Font for a font dictionary.*

### Error details

A Font dictionary has missing or invalid Type entry

* Object type: `PDFont`
* Test condition: `Type == "Font"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.1 - Table 5.8
  * PDF 1.4 Reference, 5.5.4 - Table 5.9
  * PDF 1.4 Reference, 5.6.3 - Table 5.13
  * PDF 1.4 Reference, 5.6.5 - Table 5.17

## Rule 6.3.2-2

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5.*

>*Subtype - name - (Required) The type of font; must be "Type1" for Type 1 fonts, "MMType1" for multiple master fonts, "TrueType" for TrueType fonts "Type3" for Type 3 fonts, "Type0" for Type 0 fonts and "CIDFontType0" or "CIDFontType2" for CID fonts.*

### Error details

A Font dictionary has missing or invalid Subtype entry.

The correct value of the Subtype entry in the font dictionary is critical for the text rendering in PDF documents.

* Object type: `PDFont`
* Test condition: `Subtype == "Type1" || Subtype == "MMType1" || Subtype == "TrueType" || Subtype == "Type3" || Subtype == "Type0" 
			|| Subtype == "CIDFontType0" || Subtype == "CIDFontType2"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.1 - Table 5.8
  * PDF 1.4 Reference, 5.5.4 - Table 5.9
  * PDF 1.4 Reference, 5.6.3 - Table 5.13
  * PDF 1.4 Reference, 5.6.5 - Table 5.17

## Rule 6.3.2-3

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5.*

>*BaseFont - name - (Required) The PostScript name of the font.*

### Error details

A BaseFont entry is missing or has invalid type.

For Type 1 fonts, this is usually the value of the FontName entry in the font program. The PostScript name of the font can be used to find the font’s definition in the viewer application or its environment. It is also the name that will be used when printing to a PostScript output device.

For TrueType fonts the value of BaseFont is determined in one of two ways. It is defined as the PostScript name that is an optional entry in the "name" table of the TrueType font itself. In the absence of such an entry in the "name" table, a PostScript name is derived from the name by which the font is known in the host operating system.

* Object type: `PDFont`
* Test condition: `Subtype == "Type3" || fontName != null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.1 - Table 5.8
  * PDF 1.4 Reference, 5.5.4 - Table 5.9
  * PDF 1.4 Reference, 5.6.3 - Table 5.13
  * PDF 1.4 Reference, 5.6.5 - Table 5.17

## Rule 6.3.2-4

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5.*

>*FirstChar - integer - (Required except for the standard 14 fonts) The first character code defined in the font's Widths array.*

### Error details

A non-standard simple font dictionary has missing or invalid FirstChar entry.

The PostScript names of 14 Type 1 fonts, known as the standard fonts, are as follows:
<pre>
Times−Roman      Helvetica             Courier             Symbol
Times−Bold       Helvetica−Bold        Courier−Bold        ZapfDingbats
Times−Italic     Helvetica−Oblique     Courier−Oblique
Times−BoldItalic Helvetica−BoldOblique Courier−BoldOblique
</pre>
These fonts, or their font metrics and suitable substitution fonts, are guaranteed to be available to any viewer application.

* Object type: `PDSimpleFont`
* Test condition: `isStandard == true || FirstChar != null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.1 - Table 5.8
  * PDF 1.4 Reference, 5.5.4 - Table 5.9

## Rule 6.3.2-5

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5.*

>*LastChar - integer - (Required except for the standard 14 fonts) The last character code defined in the font's Widths array.*

### Error details

A non-standard simple font dictionary has missing or invalid LastChar entry.

See also [rule 6.3.2-4](#rule-632-4).

* Object type: `PDSimpleFont`
* Test condition: `isStandard == true || LastChar != null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.1 - Table 5.8
  * PDF 1.4 Reference, 5.5.4 - Table 5.9
  * Rule 6.3.2-4

## Rule 6.3.2-6

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5.*

>*Widths - array - (Required except for the standard 14 fonts; indirect reference preferred) An array of (LastChar − FirstChar + 1) widths.*

### Error details

Widths array is missing or has invalid size.

See also [rule 6.3.2-4](#rule-632-4).

* Object type: `PDSimpleFont`
* Test condition: `isStandard == true || (Widths_size != null && Widths_size == LastChar - FirstChar + 1)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.1 - Table 5.8
  * PDF 1.4 Reference, 5.5.4 - Table 5.9
  * Rule 6.3.2-4

## Rule 6.3.2-7

### Requirement

>*All fonts used in a conforming file shall conform to the font specifications defined in PDF Reference 5.5. The subtype is the value of the Subtype key, if present, in the font file stream dictionary. The only valid values of this key in PDF 1.4 are Type1C - Type 1–equivalent font program represented in the Compact Font Format (CFF) and CIDFontType0C - Type 0 CIDFont program represented in the Compact Font Format (CFF).*

### Error details

Unsupported font file format of the embedded font.

* Object type: `PDFont`
* Test condition: `fontFileSubtype == null || fontFileSubtype == "Type1C" || fontFileSubtype == "CIDFontType0C"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.8 - Table 5.22,
  * Adobe Technical Note #5176, The Compact Font Format Specification

## Rule 6.3.3.1-1

### Requirement

>*For any given composite (Type 0) font referenced within a conforming file, the CIDSystemInfo entries of its CIDFont and CMap dictionaries shall be compatible. In other words, the Registry and Ordering strings of the CIDSystemInfo dictionaries for that font shall be identical, unless the value of the Encoding key in the font dictionary is Identity-H or Identity-V.*

### Error details

Registry and Ordering entries in the CIDFont and CMap dictionaries of a Type 0 font are not compatible.

CIDFont and CMap dictionaries contain a CIDSystemInfo entry specifying the character collection assumed by the CIDFont or by each CIDFont associated with the CMap - that is, the interpretation of the CID numbers used by the CIDFont. A character collection is uniquely identified by the Registry, Ordering, and Supplement entries in the CIDSystemInfo dictionary. Character collections whose Registry and Ordering values are the same are compatible.

PDF/A-1 standard requires that the Registry and Ordering strings of the CIDSystemInfo dictionaries for that font shall be identical, unless the value of the CMap dictionary UserCMap key is "Identity-H" or "Identity-V".

* Object type: `PDType0Font`
* Test condition: `cmapName == "Identity-H" || cmapName == "Identity-V" || (CIDFontOrdering != null && CIDFontOrdering == CMapOrdering && CIDFontRegistry != null && CIDFontRegistry == CMapRegistry)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.6.2,
  * ISO 19005-1:2005/Cor.2:2011, 6.3.3

## Rule 6.3.3.2-1

### Requirement

>*For all Type 2 CIDFonts that are used for rendering, the CIDFont dictionary shall contain a CIDToGIDMap entry that shall be a stream mapping from CIDs to glyph indices or the name Identity, as described in PDF Reference Table 5.13*

### Error details

A Type 2 CIDFont dictionary has missing or invalid CIDToGIDMap entry.

For Type 2, the CIDFont program is actually a TrueType font program, which has no native notion of CIDs. In a TrueType font program, glyph descriptions are identified by glyph index values. Glyph indices are internal to the font and are not defined consistently from one font to another. Instead, a TrueType font program contains a "cmap" table that provides mappings directly from character codes to glyph indices for one or more predefined encodings.

If the TrueType font program is embedded, the Type 2 CIDFont dictionary must contain a CIDToGIDMap entry that maps CIDs to the glyph indices for the appropriate glyph descriptions in that font program.

* Object type: `PDCIDFont`
* Test condition: `Subtype != "CIDFontType2" || CIDToGIDMap != null || renderingMode == 3`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.6.3 - Table 5.13,
  * ISO 19005-1:2005/Cor.2:2011, 6.3.3

## Rule 6.3.3.3-1

### Requirement

>*All CMaps used within a conforming file, except Identity-H and Identity-V, shall be embedded in that file as described in PDF Reference 5.6.4.*

### Error details

A CMap is different from "Identity-H" or "Identity-V" and is not embedded.

A CMap specifies the mapping from character codes to character selectors (CIDs, or character codes) in the associated CIDFont. It serves a function analogous to the Encoding dictionary for a simple font. 

A CMap may be specified in two ways: as a name object identifying a predefined CMap, whose definition is known to the viewer application, or as a stream object whose contents are an embedded CMap file. 

The "Identity−H" and "Identity−V" CMaps are two predefined CMap names that can be used to refer to characters directly by their CIDs when showing a text string.

* Object type: `PDCMap`
* Test condition: `CMapName == "Identity-H" || CMapName == "Identity-V" || containsEmbeddedFile == true`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.6.4

## Rule 6.3.3.3-2

### Requirement

>*For those CMaps that are embedded, the integer value of the WMode entry in the CMap dictionary shall be identical to the WMode value in the embedded CMap stream.*

### Error details

WMode entry in the embedded CMap and in the CMap dictionary are not identical.

A CMap also specifies the writing mode (horizontal or vertical) for any CIDFont with which the CMap is combined. This determines which metrics are to be used when glyphs are painted from that font.

In case of embedded CMap file, the writing mode is specified in two different places: in the PDF dictionary associated with the embedded CMap, and inside the embedded CMap file itself. To avoid any ambiguities, PDF/A standard requires that values of these two writing modes coincide.

* Object type: `CMapFile`
* Test condition: `WMode == dictWMode`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.3.4-1

### Requirement

>*The font programs for all fonts used within a conforming file shall be embedded within that file, as defined in PDF Reference 5.8, except when the fonts are used exclusively with text rendering mode 3.*

### Error details

The font program is not embedded.

Text rendering mode 3 specifies that glyphs are not stroked, filled or used as a clipping boundary. A font referenced for use solely in this mode is therefore not rendered and is thus exempt from the embedding requirement.

* Object type: `PDFont`
* Test condition: `Subtype == "Type3" || Subtype == "Type0" || renderingMode == 3 || containsFontFile == true`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.8

## Rule 6.3.5-1

### Requirement

>*Embedded font programs shall define all font glyphs referenced for rendering with conforming file.*

### Error details

Not all glyphs referenced for rendering are present in the embedded font program, which is used with text rendering modes other than 3.

All conforming PDF/A readers shall use the embedded fonts, rather than other locally resident, substituted or simulated fonts, for rendering.

The fonts used exclusively with text rendering mode 3 (invisible) are exempt from this requirement. OCR solutions often use such invisible fonts on top of the original scanned image to enable selection and copying of recognized text.

There is no exemption from the requirements of this rule for the 14 standard Type 1 fonts. See Rule 6.3.2-4 for the list of all standard fonts.

* Object type: `Glyph`
* Test condition: `renderingMode == 3 || isGlyphPresent == null || isGlyphPresent == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.3.5-2

### Requirement

>*For all Type 1 font subsets referenced within a conforming file, the font descriptor dictionary shall include a CharSet string listing the character names defined in the font subset, as described in PDF Reference Table 5.18.*

### Error details

A Type1 font subset does not define CharSet entry in its Descriptor dictionary.

Font subsets are acceptable by PDF/A as long as the embedded font programs provide glyph definitions for all characters referenced within the file. Embedding the font programs allows any conforming reader to reproduce correctly all glyphs in the manner in which they were originally published without reference to possibly ephemeral external resources.

* Object type: `PDType1Font`
* Test condition: `fontName.search(/[A-Z]{6}\+/) != 0 || (CharSet != null && charSetListsAllGlyphs == true)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.3

## Rule 6.3.5-3

### Requirement

>*For all CIDFont subsets referenced within a conforming file, the font descriptor dictionary shall include a CIDSet stream identifying which CIDs are present in the embedded CIDFont file, as described in PDF Reference Table 5.20.*

### Error details

A CIDSet entry in the Font descriptor is missing or does not correctly identify all glyphs present in the embedded font subset and used for rendering.

The CIDSet stream data is organized as a table of bits indexed by CID. The bits should be stored in bytes with the high-order bit first. Each bit corresponds to a CID. The first bit of the first byte corresponds to CID 0, the next bit to CID 1, and so on.

See also [rule 6.3.5-2](#rule-635-2).

* Object type: `PDCIDFont`
* Test condition: `fontName.search(/[A-Z]{6}\+/) != 0 || (containsCIDSet == true && cidSetListsAllGlyphs == true)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 5.5.3
  * Rule 6.3.5-2.

## Rule 6.3.6-1

### Requirement

>*For every font embedded in a conforming file and used for rendering, the glyph width information in the font dictionary and in the embedded font program shall be consistent.*

### Error details

Glyph width information in the embedded font program is not consistent with the Widths entry of the font dictionary.

This requirement is necessary to ensure predictable font rendering, regardless of whether a given reader uses the metrics in the Widths entry or those in the font program.

* Object type: `Glyph`
* Test condition: `renderingMode == 3 || widthFromFontProgram == null || widthFromDictionary == null || Math.abs(widthFromFontProgram - widthFromDictionary) <= 1`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1:2005/Cor.2:2011, 6.3.6

## Rule 6.3.7-1

### Requirement

>*All non-symbolic TrueType fonts shall have either MacRomanEncoding or WinAnsiEncoding as the value for the Encoding key in the Font dictionary or as the value for the BaseEncoding key in the dictionary which is the value of the Encoding key in the Font dictionary. In addition, no non-symbolic TrueType font shall define a Differences array unless all of the glyph names in the Differences array are listed in the Adobe Glyph List and the embedded font program contains at least the Microsoft Unicode (3,1 – Platform ID=3, Encoding ID=1) encoding in the 'cmap' table.*

### Error details

A non-symbolic TrueType font encoding does not define a correct mapping to the Adobe Glyph List.

Because some aspects of TrueType glyph selection are dependent on the viewer implementation or the operating system, PDF files that use TrueType fonts should follow certain guidelines to ensure predictable behavior across all viewer applications.

This requirement makes normative the suggested guidelines described in PDF 1.4 Reference 5.5.5. 

A Font is called non-symbolic, if its character set is the Adobe standard Latin character set (or a subset of it) and it uses the standard names for those characters. The characters in this character set are given in PDF 1.4 Reference, Section D.1, "Latin Character Set and Encodings."

* Object type: `PDTrueTypeFont`
* Test condition: `isSymbolic == true || ((Encoding == "MacRomanEncoding" || Encoding == "WinAnsiEncoding") && containsDifferences == false)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1:2005/Cor.2:2011, 6.3.7
  * PDF 1.4 Reference, 5.5.5.
  * PDF 1.4 Reference, Section D.1, "Latin Character Set and Encodings."

## Rule 6.3.7-2

### Requirement

>*All symbolic TrueType fonts shall not specify an Encoding entry in the font dictionary.*

### Error details

A symbolic TrueType font specifies an Encoding entry in its dictionary.

A Font is called symbolic if it contains characters outside the Adobe standard Latin character set. It is marked by special flag in its font descriptor dictionary.

* Object type: `PDTrueTypeFont`
* Test condition: `isSymbolic == false || Encoding == null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1:2005/Cor.2:2011, 6.3.7
  * PDF 1.4 Reference, 5.5.5
  * PDF 1.4 Reference, Section D.1, "Latin Character Set and Encodings."
  * Rule 6.3.7-1

## Rule 6.3.7-3

### Requirement

>*Font programs' "cmap" tables for all symbolic TrueType fonts shall contain exactly one encoding.*

### Error details

The embedded font program for a symbolic TrueType font contains more than one cmap subtable.

A TrueType font program's built-in encoding maps directly from character codes to glyph descriptions, using an internal data structure called a "cmap" (not to be confused with the CMap from Rules 6.3.3-3 and 6.3.3-4). A TrueType font program can contain multiple encodings that are intended for use on different platforms (such as Mac OS and Windows).

A Font is called symbolic if it contains characters outside the Adobe standard Latin character set. It is marked by special flag in its font descriptor dictionary. Such fonts may use implementation-specific glyph names and are required by PDF/A-1 standard to have to Encoding entry in its font dictionary (see Rule 6.3.7-2). In addition to this, the corresponding embedded TrueType program shall have only one cmap subtable to avoid ambiguities in the mapping from character codes to glyph indices in the embedded font.

* Object type: `TrueTypeFontProgram`
* Test condition: `isSymbolic == false || nrCmaps == 1`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1:2005/Cor.2:2011, 6.3.7
  * PDF 1.4 Reference, 5.5.5
  * PDF 1.4 Reference, Section D.1, "Latin Character Set and Encodings."
  * Rule 6.3.7-2

## Rule 6.3.8-1

### Requirement

>*The font dictionary shall include a ToUnicode entry whose value is a CMap stream object that maps character codes to Unicode values, as described in PDF Reference 5.9, unless the font meets any of the following three conditions:*

>-*fonts that use the predefined encodings MacRomanEncoding, MacExpertEncoding or WinAnsiEncoding, or that use the predefined Identity-H or Identity-V CMaps;*

>-*Type 1 fonts whose character names are taken from the Adobe standard Latin character set or the set of named characters in the Symbol font, as defined in PDF Reference Appendix D;*

>-*Type 0 fonts whose descendant CIDFont uses the Adobe-GB1, Adobe-CNS1, Adobe-Japan1 or Adobe-Korea1 character collections.*

### Error details

The font does not define Unicode character map

* Object type: `Glyph`
* Test condition: `toUnicode != null`
* Specification: ISO 19005-1:2005
* Levels: A

## Rule 6.4-1

### Requirement

>*If an SMask key appears in an ExtGState dictionary, its value shall be None.*

### Error details

An ExtGState contains SMask key with a value other than None.

The SMask key in an ExtGState dictionary defines the soft mask, which specifies the mask shape or mask opacity values to be used in the PDF transparent imaging model.

This provision, along with Rules 6.4-2 to 6.4-6, prohibits the use of transparency within a conforming PDF/A-1 document.

* Object type: `PDExtGState`
* Test condition: `containsSMask == false || SMaskNameValue == "None"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 7.2.6, 7.5.4

## Rule 6.4-2

### Requirement

>*An XObject dictionary shall not contain the SMask key.*

### Error details

An XObject contains an SMask key.

The SMask key in XObject dictionaries specifies a subsidiary image XObject defining a soft-mask image to be used as a source of mask shape or mask opacity values in the PDF transparent imaging model.

This provision, along with Rules 6.4-1 and 6.4-3 to 6.4-6, prohibits the use of transparency within a conforming PDF/A-1 document.

* Object type: `PDXObject`
* Test condition: `containsSMask == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1:2005/Cor.2:2011, 6.4

## Rule 6.4-3

### Requirement

>*A Group object with an S key with a value of Transparency shall not be included in a form XObject.*

>*A Group object with an S key with a value of Transparency shall not be included in a page dictionary.*

### Error details

A transparency group is present in a form XObject or page dictionary.

A group XObject is a special type of form XObject that can be used to group graphical elements together as a unit for various purposes. It is distinguished by the presence of the optional Group entry in the form dictionary. The value of this entry is a subsidiary group attributes dictionary describing the properties of the group.

A transparency group XObject is defined as a group whose S (subtype) key has value Transparency. It represents a transparency group for use in the PDF transparent imaging model.

This provision, along with Rules 6.4-1, 6.4-2, and 6.4-4 to 6.4-6, prohibits the use of transparency within a conforming PDF/A-1 document.

* Object type: `PDGroup`
* Test condition: `S != "Transparency"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1:2005/Cor.2:2011, 6.4

## Rule 6.4-4

### Requirement

>*If a BM key is present in an ExtGState object, its value shall be Normal or Compatible*

### Error details

An ExtGState dictionary contains the BM key (blend mode) with a value other than Normal or Compatible.

The BM key in the ExtGState dictionary specifies the current blend mode to be used in the PDF transparent imaging model.

The "Normal" blend mode selects the source color, ignoring the backdrop and, thus, corresponding to the traditional non-transparent imaging model. An additional standard blend mode, "Compatible", is a vestige of an earlier design and is no longer needed, but is still recognized for the sake of compatibility; its effect is equivalent to that of the "Normal" blend mode.

This provision, along with Rules 6.4-1 to 6.4-3, 6.4-5, and 6.4-6, prohibits the use of transparency within a conforming PDF/A-1 document.

* Object type: `PDExtGState`
* Test condition: `BM == null || BM == "Normal" || BM == "Compatible"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 7.2.4, 7.5.2

## Rule 6.4-5

### Requirement

>*If a CA key is present in an ExtGState object, its value shall be 1.0*

### Error details

An ExtGState dictionary contains the CA key (stroke alpha) with a value other than 1.0.

The CA key defines the current stroking alpha constant, specifying the constant shape or constant opacity value to be used for stroking operations in the PDF transparent imaging model.

This provision, along with Rules 6.4-1 to 6.4-4 and 6.4-6, prohibits the use of transparency within a conforming PDF/A-1 document.

* Object type: `PDExtGState`
* Test condition: `CA == null || CA - 1.0 < 0.000001 && CA - 1.0 > -0.000001`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 7.5.3

## Rule 6.4-6

### Requirement

>*If a ca key is present in an ExtGState object, its value shall be 1.0*

### Error details

An ExtGState dictionary contains the ca key (fill alpha) with a value other than 1.0.

The ca key defines the current non-stroking alpha constant, specifying the constant shape or constant opacity value to be used for all non-stroking operations (such as path fill or image drawing) in the PDF transparent imaging model.

This provision, along with Rules 6.4-1 to 6.4-5, prohibits the use of transparency within a conforming PDF/A-1 document.

* Object type: `PDExtGState`
* Test condition: `ca == null || ca - 1.0 < 0.000001 && ca - 1.0 > -0.000001`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 7.5.3

## Rule 6.5.2-1

### Requirement

>*Annotation types not defined in PDF Reference shall not be permitted. Additionally, the FileAttachment, Sound and Movie types shall not be permitted.*

### Error details

Unknown or not permitted annotation type.

An annotation associates an object such as a note, sound, or movie with a location on a page of a PDF document, or provides a means of interacting with the user via the mouse and keyboard. PDF includes a wide variety of standard annotation types, described in detail in PDF 1.4 Reference, 8.4.5, "Annotation Types."

Support for multimedia content is outside the scope of PDF/A-1, and, thus, annotations of type "Sound" and "Movie" are not permitted. Annotations of type "FileAttachment" are also not permitted, as PDF/A-1 does not allow any file attachments.

* Object type: `PDAnnot`
* Test condition: `Subtype == "Text" || Subtype == "Link" || Subtype == "FreeText" || Subtype == "Line" || 
			Subtype == "Square" || Subtype == "Circle" || Subtype == "Highlight" || Subtype == "Underline" ||
			Subtype == "Squiggly" || Subtype == "StrikeOut" || Subtype == "Stamp" || Subtype == "Ink" ||
			Subtype == "Popup" || Subtype == "Widget" || Subtype == "PrinterMark" || Subtype == "TrapNet"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 8.4.5

## Rule 6.5.3-1

### Requirement

>*An annotation dictionary shall not contain the CA key with a value other than 1.0.*

### Error details

An annotation dictionary contains the CA key with value other than 1.0.

The CA key specifies the constant opacity value to be used in painting the annotation. It shall have the value 1.0 similar to stroking and non-stroking constant alpha in the ExtGState dictionary (see Rules 6.4-5 and 6.4-6).

* Object type: `PDAnnot`
* Test condition: `CA == null || CA == 1.0`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 7.2.6
  * Rules 6.4-5, 6.4-6

## Rule 6.5.3-2

### Requirement

>*An annotation dictionary shall contain the F key. The F key's Print flag bit shall be set to 1 and its Hidden, Invisible and NoView flag bits shall be set to 0.*

### Error details

Annotation flags are either missing or set the annotation to be hidden/invisible or non-printable.

The value of the annotation dictionary's F entry is an unsigned 32-bit integer containing flags specifying various characteristics of the annotation. Bit positions within the flag word are numbered from 1 (low-order) to 32 (high-order).

Flag "Invisible" (bit position 1), if clear, allows to display an unknown annotation using an appearance stream specified by its appearance dictionary, if any.

Flag "Hidden" (bit position 2), if set, specifies to not display or print the annotation or allow it to interact with the user, regardless of its annotation type.

Flag "Print" (bit position 3), if set, specifies to print the annotation when the page is printed.

Flag "NoView" (bit position 6), if set, specifies to not display the annotation on the screen or allow it to interact with the user.

The restrictions on annotation flags prevent the use of annotations that are hidden or that are viewable but not printable.

* Object type: `PDAnnot`
* Test condition: `F != null && (F & 4) == 4 && (F & 1) == 0 && (F & 2) == 0 && (F & 32) == 0`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.5.3-3

### Requirement

>*An annotation dictionary shall not contain the C array or the IC array unless the colour space of the DestOutputProfile in the PDF/A-1 OutputIntent dictionary, defined in 6.2.2, is RGB.*

### Error details

Annotation's color or interior color is used without specifying RGB-based destination output profile.

These provisions ensure that the device colour spaces used in annotations by mechanisms other than an appearance stream are indirectly defined by means of the PDF/A-1 OutputIntent. See also [rule 6.2.3-2](#rule-623-2).

* Object type: `PDAnnot`
* Test condition: `(containsC == false && containsIC == false) || gOutputCS == "RGB "`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.5.3-4

### Requirement

>*For all annotation dictionaries containing an AP key, the appearance dictionary that it defines as its value shall contain only the N key.*

### Error details

Annotation's appearance dictionary contains entries other than N

An annotation can define as many as three separate appearances. The normal appearance (N key in the appearance dictionary) is used when the annotation is not interacting with the user. This is also the appearance that is used for printing the annotation. The rollover appearance (R key) is used when the user moves the cursor into the annotation's active area without pressing the mouse button. The down appearance (R key) is used when the mouse button is pressed or held down within the annotation's active area.

In accordance with the PDF 1.4 Specification, 8.4.4, a Button form field needs to have multiple appearance states, each one associated with the specific values that the button can take.

### PDF Validation Technical Working Group notes

Even if a Button form field has only one state, it is still required to have an appearance subdictionary with a single key as its default (and only) state.

* Object type: `PDAnnot`
* Test condition: `AP == null || AP == "N"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO19005-1/Cor.2:2011, 6.5.3

## Rule 6.5.3-5

### Requirement

>*If an annotation dictionary's Subtype key has a value of Widget and its FT key has a value of Btn, the value of the N key shall be an appearance subdictionary.*

### Error details

An annotation dictionary's Subtype key has a value of Widget and its FT key has a value of Btn, but the value of the N key is different from appearance subdictionary

See also [rule 6.5.3-4](#rule-653-4).

* Object type: `PDAnnot`
* Test condition: `AP != "N" || Subtype != "Widget" || FT != "Btn" || (N_type == "Dict" && containsAppearances == true)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO19005-1/Cor.2:2011, 6.5.3

## Rule 6.5.3-6

### Requirement

>*If an annotation dictionary's Subtype key has value other than Widget, or if FT key associated with Widget annotation has value other than Btn, the value of the N key shall be an appearance stream.*

### Error details

An annotation dictionary's Subtype key has a value other than Widget or/and its FT key different from Btn, but the value of the N key is not an appearance stream

See also [rule 6.5.3-4](#rule-653-4).

* Object type: `PDAnnot`
* Test condition: `AP != "N" || (Subtype == "Widget" && FT == "Btn") || N_type == "Stream"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO19005-1/Cor.2:2011, 6.5.3

## Rule 6.6.1-1

### Requirement

>*The Launch, Sound, Movie, ResetForm, ImportData and JavaScript actions shall not be permitted. Additionally, the deprecated set-state and no-op actions shall not be permitted. The Hide action shall not be permitted.*

### Error details

Unknown or not permitted action type.

Support for multimedia content is outside the scope of PDF/A-1. The ResetForm action changes the rendered appearance of a form. The ImportData action imports form data from an external file. JavaScript actions permit an arbitrary executable code that has the potential to interfere with reliable and predictable rendering.

* Object type: `PDAction`
* Test condition: `S == "GoTo" || S == "GoToR" || S == "Thread" || S == "URI" || S == "Named" || S == "SubmitForm"`
* Specification: ISO 19005-1:2005/Corr.2:2011
* Levels: A, B
* Additional references: 
  * ISO 19005-1:2005/Cor.2:2011, 6.6.1


## Rule 6.6.1-2

### Requirement

>*Named actions other than NextPage, PrevPage, FirstPage, and LastPage shall not be permitted.*

### Error details

Unknown named action.

In response to each of the four allowed named actions, conforming interactive readers shall perform the appropriate action described in PDF 1.4 Reference Table 8.45. Viewer applications may support additional, nonstandard named actions, but any document using them will not be portable.

* Object type: `PDNamedAction`
* Test condition: `N == "NextPage" || N == "PrevPage" || N == "FirstPage" || N == "LastPage"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * PDF 1.4 Reference, 8.5.3 - Named Actions - Table 8.45

## Rule 6.6.1-3

### Requirement

>*Interactive form fields shall not perform actions of any type.*

### Error details

Interactive form field contains an action object ('A' key).

Such actions may modify the values of the forms and alter the visual representation of the document.

* Object type: `PDWidgetAnnot`
* Test condition: `containsA == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.6.2-1

### Requirement

>*A Widget annotation dictionary shall not include an AA entry for an additional-actions dictionary.*

### Error details

A Widget annotation contains an additional-actions dictionary (AA entry).

* Object type: `PDWidgetAnnot`
* Test condition: `containsAA == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.6.2-2

### Requirement

>*A Field dictionary shall not include an AA entry for an additional-actions dictionary.*

### Error details

A Field dictionary contains an additional-actions dictionary (AA entry).

These additional-actions dictionaries define arbitrary JavaScript actions. The explicit prohibition of the AA entry has the implicit effect of disallowing JavaScript actions that can create external dependencies and complicate preservation efforts.

* Object type: `PDFormField`
* Test condition: `containsAA == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.6.2-3

### Requirement

>*The document catalog dictionary shall not include an AA entry for an additional-actions dictionary.*

### Error details

The document catalog dictionary contains an additional-actions dictionary (AA entry).

These additional-actions dictionaries define arbitrary JavaScript actions. The explicit prohibition of the AA entry has the implicit effect of disallowing JavaScript actions that can create external dependencies and complicate preservation efforts.

* Object type: `PDDocument`
* Test condition: `containsAA == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.2-1

### Requirement

>*The document catalog dictionary of a conforming file shall contain the Metadata key. The metadata stream dictionary shall contain entry Type with value /Metadata and entry Subtype with value /XML*

### Error details

The document catalog dictionary doesn't contain Metadata key or metadata stream dictionary does not contain either entry Type with value /Metadata or entry Subtype with value /XML.

Metadata is essential for effective management of a file throughout its life cycle. A file depends on metadata for identification and description, as well as for describing appropriate technical and administrative matters.

Metadata, both for an entire document and for components within a document, can be stored in PDF streams called metadata streams. The contents of a metadata stream is the metadata represented in Extensible Markup Language (XML). The format of the XML representing the metadata is defined as part of a framework called the Extensible Metadata Platform (XMP).

A metadata stream can be attached to a document through the Metadata entry in the document catalog.

* Object type: `PDDocument`
* Test condition: `containsMetadata == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.2-2

### Requirement

>*Metadata object stream dictionaries shall not contain the Filter key.*

### Error details

The metadata object stream dictionary contains the Filter key.

The explicit prohibition of the Filter key has the implicit effect of preserving the contents of XMP metadata streams as plain text that is visible to non-PDF aware tools.

### PDF Validation Technical Working Group notes

All requirements in the PDF/A-1 standard pertaining to XMP metadata address only the document level XMP package (the /Metadata entry in the document Catalog), and font metadata. But the ISO 19005-1 clause 6.7.2 "Metadata object stream dictionaries shall not contain the Filter key" explicitly requires all XMP Metadata streams in PDF/A-1 documents to be uncompressed, that is contain no /Filter key. 

However, all other non-document Metadata streams may not conform to XMP or even to XML format. 

* Object type: `PDMetadata`
* Test condition: `Filter == null`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.3-1

### Requirement

>*The value of CreationDate entry from the document information dictionary, if present, and its analogous XMP property "xmp:CreateDate" shall be equivalent.*

### Error details

The value of CreationDate entry from the document Info dictionary and its matching XMP property "xmp:CreateDate" are not equivalent

The explicit requirement for equivalence between the values of document information dictionary entries and their analogous XMP properties has the implicit effect of providing unambiguous interpretation of that property's value.

* Object type: `CosInfo`
* Test condition: `doCreationDatesMatch != false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-2

### Requirement

>*The value of Title entry from the document information dictionary, if present, and its analogous XMP property "dc:title['x-default']" shall be equivalent.*

### Error details

The value of Title entry from the document Info dictionary and its matching XMP property "dc:title['x-default']" are not equivalent

* Object type: `CosInfo`
* Test condition: `Title == null || Title == XMPTitle`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-3

### Requirement

>*The value of Author entry from the document information dictionary, if present, and its analogous XMP property "dc:creator" shall be equivalent. dc:creator shall contain exactly one entry*

### Error details

The value of Author entry from the document Info dictionary and its matching XMP property "dc:creator" are not equivalent

* Object type: `CosInfo`
* Test condition: `Author == null || (Author == XMPCreator && XMPCreatorSize == 1)`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-4

### Requirement

>*The value of Subject entry from the document information dictionary, if present, and its analogous XMP property "dc:description['x-default']" shall be equivalent.*

### Error details

The value of Subject entry from the document Info dictionary and its matching XMP property "dc:description['x-default']" are not equivalent

* Object type: `CosInfo`
* Test condition: `Subject == null || Subject == XMPDescription`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-5

### Requirement

>*The value of Keywords entry from the document information dictionary, if present, and its analogous XMP property "pdf:Keywords" shall be equivalent.*

### Error details

The value of Keywords entry from the document Info dictionary and its matching XMP property "pdf:Keywords" are not equivalent

* Object type: `CosInfo`
* Test condition: `Keywords == null || Keywords == XMPKeywords`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-6

### Requirement

>*The value of Creator entry from the document information dictionary, if present, and its analogous XMP property "xmp:CreatorTool" shall be equivalent.*

### Error details

The value of Creator entry from the document Info dictionary and its matching XMP property "xmp:CreatorTool" are not equivalent

* Object type: `CosInfo`
* Test condition: `Creator == null || Creator == XMPCreatorTool`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-7

### Requirement

>*The value of Producer entry from the document information dictionary, if present, and its analogous XMP property "pdf:Producer" shall be equivalent.*

### Error details

The value of Producer entry from the document Info dictionary and its matching XMP property "pdf:Producer" are not equivalent

* Object type: `CosInfo`
* Test condition: `Producer == null || Producer == XMPProducer`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.3-8

### Requirement

>*The value of ModDate entry from the document information dictionary, if present, and its analogous XMP property "xmp:ModifyDate" shall be equivalent.*

### Error details

The value of ModDate entry from the document Info dictionary and its matching XMP property "xmp:ModifyDate" are not equivalent

* Object type: `CosInfo`
* Test condition: `doModDatesMatch != false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007/Cor.2:2011, 6.7.3

## Rule 6.7.5-1

### Requirement

>*The bytes attribute shall not be used in the header of an XMP packet.*

### Error details

The XMP Package contains bytes attribute.

Both the bytes and encoding attributes are deprecated in XMP Specification.

* Object type: `XMPPackage`
* Test condition: `bytes == null`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.5-2

### Requirement

>*The encoding attribute shall not be used in the header of an XMP packet.*

### Error details

The XMP Package contains encoding attribute.

Both the bytes and encoding attributes are deprecated in XMP Specification.

* Object type: `XMPPackage`
* Test condition: `encoding == null`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-1

### Requirement

>*[Extension schema] descriptions shall be specified using the PDF/A extension schema container schema defined in this clause.*

### Error details

An extension schema object contains fields not defined by the specification.

An extension schema is any XMP schema that is not defined in the XMP Specification. All objects of the extension schemas and their fields are specified in ISO 19005-1:2005, 6.7.8 and its technical corrigenda. 

* Object type: `ExtensionSchemaObject`
* Test condition: `containsUndefinedFields == false`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-2

### Requirement

>*The extension schema container schema … uses the namespace URI "http://www.aiim.org/pdfa/ns/extension/". The required schema namespace prefix is pdfaExtension. [The container schema includes the following property: pdfaExtension:schemas (Bag).]*

### Error details

The extension schema container has invalid type or doesn't have prefix 'pdfaExtension'.

Extension schema container stores a sequence of extension schemas defined in the XMP package.

* Object type: `ExtensionSchemasContainer`
* Test condition: `isValidBag == true && prefix == "pdfaExtension"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-3

### Requirement

>*Field 'schema' of the PDF/A Schema value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaSchema'.*

### Error details

Field 'schema' of the PDF/A Schema value type is not present, or doesn't have prefix 'pdfaSchema' or doesn't have type Text

* Object type: `ExtensionSchemaDefinition`
* Test condition: `isSchemaValidText == true && schemaPrefix == "pdfaSchema"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-4

### Requirement

>*Field 'namespaceURI' of the PDF/A Schema value type in the PDF/A extension schema shall be present and shall have type URI and namespace prefix 'pdfaSchema'.*

### Error details

Field 'namespaceURI' of the PDF/A Schema value type is not present, or doesn't have prefix 'pdfaSchema' or doesn't have type URI

* Object type: `ExtensionSchemaDefinition`
* Test condition: `isNamespaceURIValidURI == true && namespaceURIPrefix == "pdfaSchema"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-5

### Requirement

>*Field 'prefix' of the PDF/A Schema value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaSchema'.*

### Error details

Field 'prefix' of the PDF/A Schema value type is not present, or doesn't have prefix 'pdfaSchema' or doesn't have type Text

* Object type: `ExtensionSchemaDefinition`
* Test condition: `isPrefixValidText == true && prefixPrefix == "pdfaSchema"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-6

### Requirement

>*Field 'property' of the PDF/A Schema value type in the PDF/A extension schema be present and shall have type Seq Property and namespace prefix 'pdfaSchema'.*

### Error details

Field 'property' of the PDF/A Schema value type is not present, or doesn't have prefix 'pdfaSchema' or doesn't have type Seq Property

* Object type: `ExtensionSchemaDefinition`
* Test condition: `isPropertyValidSeq == true && propertyPrefix == "pdfaSchema"`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-7

### Requirement

>*Field 'valueType' of the PDF/A Schema value type in the PDF/A extension schema shall have type Seq ValueType and namespace prefix 'pdfaSchema'.*

### Error details

Field 'valueType' of the PDF/A Schema value type doesn't have prefix 'pdfaSchema' or doesn't have type Seq ValueType

* Object type: `ExtensionSchemaDefinition`
* Test condition: `isValueTypeValidSeq == true && (valueTypePrefix == null || valueTypePrefix == "pdfaSchema")`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * ISO 19005-1:2005/Cor.1:2007, 6.7.8

## Rule 6.7.8-8

### Requirement

>*Field 'name' of the PDF/A Property value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaProperty'.*

### Error details

Field 'name' of the PDF/A Property value type is not present, or doesn't have prefix 'pdfaProperty' or doesn't have type Text

* Object type: `ExtensionSchemaProperty`
* Test condition: `isNameValidText == true && namePrefix == "pdfaProperty"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-9

### Requirement

>*Field 'valueType' of the PDF/A Property value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaProperty'. Value of valueType property shall be defined.*

### Error details

Field 'valueType' of the PDF/A Property value type is not present, or doesn't have prefix 'pdfaProperty' or doesn't have type Text or value of valueType doesn't defined

* Object type: `ExtensionSchemaProperty`
* Test condition: `isValueTypeValidText == true && isValueTypeDefined == true && valueTypePrefix == "pdfaProperty"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-10

### Requirement

>*Field 'category' of the PDF/A Property value type in the PDF/A extension schema shall be present and shall have type Text, value external or internal and namespace prefix 'pdfaProperty'.*

### Error details

Field 'category' of the PDF/A Property value type is not present, or doesn't have prefix 'pdfaProperty' or doesn't have type Text (isCategoryValidText = %2) or has value different from external or internal

* Object type: `ExtensionSchemaProperty`
* Test condition: `isCategoryValidText == true && (category == "external" || category == "internal") && categoryPrefix == "pdfaProperty"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-11

### Requirement

>*Field 'description' of the PDF/A Property value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaProperty'.*

### Error details

Field 'description' of the PDF/A Property value type is not present, or doesn't have prefix 'pdfaProperty' or doesn't have type Text

* Object type: `ExtensionSchemaProperty`
* Test condition: `isDescriptionValidText == true && descriptionPrefix == "pdfaProperty"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-12

### Requirement

>*Field 'type' of the PDF/A ValueType value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaType'.*

### Error details

Field 'type' of the PDF/A ValueType value type is not present, or doesn't have prefix 'pdfaType' or doesn't have type Text

* Object type: `ExtensionSchemaValueType`
* Test condition: `isTypeValidText == true && typePrefix == "pdfaType"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-13

### Requirement

>*Field 'namespaceURI' of the PDF/A ValueType value type in the PDF/A extension schema shall be present and shall have type URI and namespace prefix 'pdfaType'.*

### Error details

Field 'namespaceURI' of the PDF/A ValueType value type is not present, or doesn't have prefix 'pdfaType' or doesn't have type URI

* Object type: `ExtensionSchemaValueType`
* Test condition: `isNamespaceURIValidURI == true && namespaceURIPrefix == "pdfaType"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-14

### Requirement

>*Field 'prefix' of the PDF/A ValueType value type in the PDF/A extension schema be present and shall have type Text and namespace prefix 'pdfaType'.*

### Error details

Field 'prefix' of the PDF/A ValueType value type is not present, or doesn't have prefix 'pdfaType' or doesn't have type Text

* Object type: `ExtensionSchemaValueType`
* Test condition: `isPrefixValidText == true && prefixPrefix == "pdfaType"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-15

### Requirement

>*Field 'description' of the PDF/A ValueType value type in the PDF/A extension schema shall be present and shall have type Text and namespace prefix 'pdfaType'.*

### Error details

Field 'description' of the PDF/A ValueType value type is not present, or doesn't have prefix 'pdfaType' or doesn't have type Text

* Object type: `ExtensionSchemaValueType`
* Test condition: `isDescriptionValidText == true && descriptionPrefix == "pdfaType"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-16

### Requirement

>*Field 'field' of the PDF/A ValueType value type in the PDF/A extension schema shall have type Seq Field and namespace prefix 'pdfaType'.*

### Error details

Field 'field' of the PDF/A ValueType value type doesn't have prefix 'pdfaType' or doesn't have type Seq Field

* Object type: `ExtensionSchemaValueType`
* Test condition: `isFieldValidSeq == true && (fieldPrefix == null || fieldPrefix == "pdfaType")`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-17

### Requirement

>*Field 'name' of the PDF/A Field value type in the PDF/A extension schema shall have type Text and namespace prefix 'pdfaField'.*

### Error details

Field 'name' of the PDF/A Field value type is not present, or doesn't have prefix 'pdfaField' or doesn't have type Text

* Object type: `ExtensionSchemaField`
* Test condition: `isNameValidText == true && namePrefix == "pdfaField"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-18

### Requirement

>*Field 'valueType' of the PDF/A Field value type in the PDF/A extension schema shall have type Text and namespace prefix 'pdfaField'. Value of valueType property shall be defined.*

### Error details

Field 'valueType' of the PDF/A Field value type is not present, or doesn't have prefix 'pdfaField' or doesn't have type Text or value of valueType doesn't defined

* Object type: `ExtensionSchemaField`
* Test condition: `isValueTypeValidText == true && isValueTypeDefined == true && valueTypePrefix == "pdfaField"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.8-19

### Requirement

>*Field 'description' of the PDF/A Field value type in the PDF/A extension schema shall have type Text and namespace prefix 'pdfaField'.*

### Error details

Field 'description' of the PDF/A Field value type is not present, or doesn't have prefix 'pdfaField' or doesn't have type Text

* Object type: `ExtensionSchemaField`
* Test condition: `isDescriptionValidText == true && descriptionPrefix == "pdfaField"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.9-1

### Requirement

>*The metadata stream shall conform to XMP Specification and well formed PDFAExtension Schema for all extensions.*

### Error details

The serialization of the metadata stream does not conform to XMP Specification.

* Object type: `XMPPackage`
* Test condition: `isSerializationValid`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references: 
  * XMP Specification January 2004

## Rule 6.7.9-2

### Requirement

>*Properties specified in XMP form shall use either the predefined schemas defined in XMP Specification, or extension schemas that comply with XMP Specification*

### Error details

A property is either not defined in XMP Specification, or is not defined in any of the extension schemas.

* Object type: `XMPProperty`
* Test condition: `isPredefinedInXMP2004 == true || isDefinedInCurrentPackage == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.9-3

### Requirement

>*Properties specified in XMP form shall use either the predefined schemas defined in XMP Specification, or extension schemas that comply with XMP Specification*

### Error details

XMP property does not correspond to defined type

* Object type: `XMPProperty`
* Test condition: `isValueTypeCorrect == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.11-1

### Requirement

>*The PDF/A version and conformance level of a file shall be specified using the PDF/A Identification extension schema.*

### Error details

The document metadata stream does not contain a PDF/A Identification Schema.

* Object type: `MainXMPPackage`
* Test condition: `containsPDFAIdentification == true`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.11-2

### Requirement

>*The value of "pdfaid:part" shall be the part number of ISO 19005 to which the file conforms.*

### Error details

The "part" property of the PDF/A Identification Schema is not equal to 1 for a PDF/A-1 conforming file.

* Object type: `PDFAIdentification`
* Test condition: `part == 1`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.11-3

### Requirement

>*A Level A conforming file shall specify the value of "pdfaid:conformance" as A. A Level B conforming file shall specify the value of "pdfaid:conformance" as B.*

### Error details

The "conformance" property of the PDF/A Identification Schema is not equal to "A" for a PDF/A-1a conforming file.

The "conformance" property of the PDF/A Identification Schema is not equal to "A" or "B" for a PDF/A-1b conforming file.

As Level B requirements form a strict subset of Level A requirements, validation for Level B conformance also accepts the "conformance" property having a value of "A".

* PDF/A-1a validation:
  * Object type: `PDFAIdentification`
  * Test condition: `conformance == "A"`
  * Specification: ISO 19005-1:2005
  * Levels: A

* PDF/A-1b validation:
  * Object type: `PDFAIdentification`
  * Test condition: `conformance == "A" || conformance == "B"`
  * Specification: ISO 19005-1:2005
  * Levels: B

## Rule 6.7.11-4

### Requirement

>*Property "part" of the PDF/A Identification Schema shall have namespace prefix "pdfaid"*

### Error details

Property "part" of the PDF/A Identification Schema has an invalid namespace prefix

* Object type: `PDFAIdentification`
* Test condition: `partPrefix == null || partPrefix == "pdfaid"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.11-5

### Requirement

>*Property "conformance" of the PDF/A Identification Schema shall have namespace prefix "pdfaid"*

### Error details

Property "conformance" of the PDF/A Identification Schema has an invalid namespace prefix

* Object type: `PDFAIdentification`
* Test condition: `conformancePrefix == null || conformancePrefix == "pdfaid"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.7.11-6

### Requirement

>*Property "amd" of the PDF/A Identification Schema shall have namespace prefix "pdfaid"*

### Error details

Property "amd" of the PDF/A Identification Schema has an invalid namespace prefix

* Object type: `PDFAIdentification`
* Test condition: `amdPrefix == null || amdPrefix == "pdfaid"`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.8.2.2-1

### Requirement

>*The document catalog dictionary shall include a MarkInfo dictionary with a Marked entry in it, whose value shall be true.*

### Error details

MarkInfo dictionary is not present in the document catalog, or Marked entry is set to false or is not present in the MarkInfo dictionary.

This setting indicates that the file conforms to the Tagged PDF conventions.

* Object type: `CosDocument`
* Test condition: `Marked == true`
* Specification: ISO 19005-1:2005
* Levels: A
* Additional references: 
  * PDF 1.4 Reference, 9.7.1

## Rule 6.8.3.3-1

### Requirement

>*The logical structure of the conforming file shall be described by a structure hierarchy rooted in the StructTreeRoot entry of the document catalog dictionary, as described in PDF Reference 9.6.*

### Error details

StructTreeRoot entry is not present in the document catalog.

The logical structure of a document is described by a hierarchy of objects called the structure hierarchy or structure tree. At the root of the hierarchy is a dictionary object called the structure tree root, located via the StructTreeRoot entry in the document catalog.

* Object type: `PDDocument`
* Test condition: `containsStructTreeRoot == true`
* Specification: ISO 19005-1:2005
* Levels: A
* Additional references: 
  * PDF 1.4 Reference, 9.6.1

## Rule 6.8.3.4-1

### Requirement

>*All non-standard structure types shall be mapped to the nearest functionally equivalent standard type, as defined in PDF Reference 9.7.4, in the role map dictionary of the structure tree root.*

### Error details

Non-standard structure type is not mapped to a standard type.

* Object type: `SENonStandard`
* Test condition: `isNotMappedToStandardType == false`
* Specification: ISO 19005-1:2005
* Levels: A
* Additional references: 
  * PDF 1.4 Reference, 9.7.4

## Rule 6.8.3.4-2

### Requirement

>*A circular mapping shall not exist.*

### Error details

A circular mapping exists.

* Object type: `SENonStandard`
* Test condition: `circularMappingExist != true`
* Specification: ISO 19005-1:2005
* Levels: A
* Additional references: 
  * PDF 1.4 Reference, 9.7.4

## Rule 6.8.4-1

### Requirement

>*If the Lang entry is present in the document catalog dictionary or in a structure element dictionary or property list, its value shall be a language identifier as defined by RFC 1766, Tags for the Identification of Languages, as described in PDF Reference 9.8.1.*

### Error details

Value of the Lang entry is not a Language-Tag

* Object type: `CosLang`
* Test condition: `unicodeValue == '' || /^[a-zA-Z]{1,8}(-[a-zA-Z]{1,8})*$/.test(unicodeValue)`
* Specifications: ISO 19005-1:2005
* Levels: A
* Additional references:
  * PDF Reference 1.4, 9.8.1,
  * RFC 1766, 2

## Rule 6.9-1

### Requirement

>*The NeedAppearances flag of the interactive form dictionary shall either not be present or shall be false.*

### Error details

The interactive form dictionary contains the NeedAppearances flag with value true.

This flag specifies whether to construct appearance streams and appearance dictionaries for all widget annotations in the document.

Every form field shall have an appearance dictionary associated with the field's data. A conforming reader shall render the field according to the appearance dictionary without regard to the form data. Requiring an appearance dictionary ensures the reliable rendering of the form.

* Object type: `PDAcroForm`
* Test condition: `NeedAppearances == null || NeedAppearances == false`
* Specification: ISO 19005-1:2005
* Levels: A, B

## Rule 6.9-2

### Requirement

>*Every form field shall have an appearance dictionary associated with the field's data.*

### Error details

A form field does not have an appearance dictionary associated with the field's data.

A conforming reader shall render the field according to the appearance dictionary without regard to the form data. Requiring an appearance dictionary ensures the reliable rendering of the form.

Interactive forms use widget annotations to represent the appearance of fields and to manage user interactions.

* Object type: `PDWidgetAnnot`
* Test condition: `AP != null`
* Specification: ISO 19005-1:2005
* Levels: A, B
* Additional references:
  * PDF 1.4 Reference, 8.4.5, Widget Annotations
