package parsers

// Parser interface for all website parsers.
// Each parser can fetch + parse data differently (HTML scraping, API calls, etc.)
type Parser interface {
	// CanHandle checks if this parser can handle the given URL
	CanHandle(url string) bool

	// NormalizeURL transforms the URL before processing.
	// Returns the normalized URL and an extracted slug (if applicable).
	NormalizeURL(url string) (normalizedURL string, slug string)

	// NeedsHTML returns true if this parser expects raw HTML content
	// to be fetched by the handler. If false, the parser handles
	// its own data fetching via FetchAndParse.
	NeedsHTML() bool

	// Parse extracts data from raw HTML content.
	// Only called when NeedsHTML() returns true.
	Parse(html string) (map[string]interface{}, error)

	// FetchAndParse fetches data from the URL and returns structured data.
	// Only called when NeedsHTML() returns false.
	// The parser handles its own HTTP calls (API, HEAD request, etc.)
	FetchAndParse(url string) (map[string]interface{}, error)

	// GetName returns the parser name
	GetName() string
}

// ParserRegistry holds all registered parsers
type ParserRegistry struct {
	parsers []Parser
}

// NewRegistry creates a new parser registry
func NewRegistry() *ParserRegistry {
	return &ParserRegistry{
		parsers: make([]Parser, 0),
	}
}

// Register adds a parser to the registry
func (r *ParserRegistry) Register(parser Parser) {
	r.parsers = append(r.parsers, parser)
}

// FindParser finds a parser that can handle the given URL
func (r *ParserRegistry) FindParser(url string) Parser {
	for _, parser := range r.parsers {
		if parser.CanHandle(url) {
			return parser
		}
	}
	return nil
}

// GetAllParsers returns all registered parsers
func (r *ParserRegistry) GetAllParsers() []Parser {
	return r.parsers
}
