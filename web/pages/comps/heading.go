package comps

import "github.com/rohanthewiz/element"

type Heading struct {
	Title string
}

func (h Heading) Render(b *element.Builder) (x any) {
	// Add the page heading after the components
	// c.Heading accesses the ContactPage's Heading field
	b.H1("style", "color:maroon;background-color:#dfc673").T(h.Title)
	return
}
