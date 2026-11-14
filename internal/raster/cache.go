package raster

import (
	"container/list"
	"image"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Cache keeps what rendering derives from a document and the next render of
// the same document needs again: parsed fonts, and decoded images up to a
// memory budget. A viewer that draws a page in tiles, or again at another
// zoom, would otherwise decode every picture and parse every font each time.
//
// Entries are keyed by the identity of the document's own objects, which the
// document keeps alive and keeps one copy of. A Cache is not safe for
// concurrent use; the document's render lock serializes it.
type Cache struct {
	fonts   map[uintptr]*fontInfo
	images  map[uintptr]*list.Element // of *cachedImage
	byImage map[*image.RGBA]*cachedImage
	order   *list.List // most recently used first
	bytes   int
	limit   int
}

type cachedImage struct {
	key  uintptr
	dict pdf.PDFDict // held so the key's object stays alive and unique
	img  *image.RGBA
	s    *sampler // its mip pyramid, built on first use and kept with it
	cost int
}

// NewCache returns a cache that holds at most limit bytes of decoded images.
func NewCache(limit int) *Cache {
	return &Cache{fonts: map[uintptr]*fontInfo{}, images: map[uintptr]*list.Element{}, byImage: map[*image.RGBA]*cachedImage{}, order: list.New(), limit: limit}
}

// decode returns the decoded image of dict. Only successfully decoded images
// are kept, and an image larger than the budget is not.
func (c *Cache) decode(dict, resources pdf.PDFDict) (*image.RGBA, error) {
	key := pdf.ValuePointer(dict.Entries)
	if e, ok := c.images[key]; ok {
		c.order.MoveToFront(e)
		return e.Value.(*cachedImage).img, nil
	}
	img, err := DecodeImageRGBA(dict, resources)
	if err != nil {
		return img, err
	}
	// A pyramid adds a third to the pixels it is built from.
	cost := len(img.Pix) + len(img.Pix)/3
	if cost > c.limit {
		return img, nil
	}
	e := &cachedImage{key: key, dict: dict, img: img, cost: cost}
	c.images[key] = c.order.PushFront(e)
	c.byImage[img] = e
	c.bytes += cost
	for c.bytes > c.limit {
		old := c.order.Remove(c.order.Back()).(*cachedImage)
		delete(c.images, old.key)
		delete(c.byImage, old.img)
		c.bytes -= old.cost
	}
	return img, nil
}

// sampler returns the sampler of img, which keeps its pyramid across renders
// when img is one the cache decoded, and is new otherwise.
func (c *Cache) sampler(img *image.RGBA) *sampler {
	e := c.byImage[img]
	if e == nil {
		return newSampler(img)
	}
	if e.s == nil {
		e.s = newSampler(img)
	}
	return e.s
}
