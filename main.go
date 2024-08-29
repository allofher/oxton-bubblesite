package main

import (
	"context"
	"errors"
	"strings"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	"embed"
	"io/fs"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
	"github.com/muesli/termenv"
)

// TODO: turn existing articles into markdown
// TOOD: "refactor" to separate files out
// TODO: finish styling
// TODO: github
// TODO: update home page bio (for reg website too)
// TODO: your 'drafts' aren't drafts anymore
// TODO: clean up environment stuff for 'release'
// TODO: how to host online???

const (
	host = "localhost"
	port = "23234"
	statusBarHeight = 1
	helpBarHeight = 1
)

const (
	LOADING = iota
	ARTICLE
	HOME
)

type model struct {
	currentState    int
	width           int
	height          int
	time            time.Time
	style           lipgloss.Style
	loadProgress    float64
	loadBar         progress.Model
	articleList     list.Model
	articleViewport viewport.Model
	currentArticle  article
}

//go:embed homeBio.txt
var homeBio string

//go:embed articles
var articlesFS embed.FS

type timeMsg time.Time
type tickMsg time.Time
type contentRenderedMsg string
type errMsg struct { err error }

//list stuff
type article struct {
	title string
	path string
	body string
	description string
}
func (a article) Title() string { return a.title }
func (a article) Path() string { return a.path }
func (a article) Body() string { return a.body }
func (a article) Description() string { return a.description }
func (a article) FilterValue() string { return a.path }

// lipgloss style definitions
var (

	// re-used font strengths
	subtleStyle   = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	// highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	// special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	
	// Loading Bar
	outlineBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(1, 0).
			BorderTop(true).
			BorderLeft(true).
			BorderRight(true).
			BorderBottom(true)
	
	// Home Bio
	bioStyle = lipgloss.NewStyle().
			Align(lipgloss.Center).
			Foreground(lipgloss.Color("#FAFAFA")).
			Margin(1, 3, 0, 0).
			Padding(1, 2)

	// Article Viewport Container
	articleViewportStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(0, 0).
			BorderTop(true).
			BorderLeft(true).
			BorderRight(true).
			BorderBottom(true)
	
	// Home List
	listTitleStyle        = lipgloss.NewStyle().MarginLeft(2)
	listItemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	listActiveItemStyle   = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
	listPaginationStyle   = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	listHelpStyle         = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)

	// Status Bar
	statusNuggetStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#343433")).
		Background(lipgloss.Color("#AF9CA2"))

	statusStyle = lipgloss.NewStyle().
			Inherit(statusBarStyle).
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#8D6888")).
			Padding(0, 2).
			MarginRight(1)

	statusTextStyle = lipgloss.NewStyle().Inherit(statusBarStyle)
	
	// Page.
	docStyle = lipgloss.NewStyle().Padding(1, 2, 1, 2)

)

// TODO: needs dynamic spacing of characters
// TODO: needs more personal touch (eg. replace bg char)
func composeLoadingBar(m *model) string {
	loadingBar := lipgloss.NewStyle().
		Width(int(float64(m.width) * .25)).
		Align(lipgloss.Center).
		Render(m.loadBar.ViewAs(m.loadProgress))
	barWithOutline := lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		outlineBoxStyle.Render(loadingBar),
		lipgloss.WithWhitespaceChars("猫咪"),
		lipgloss.WithWhitespaceForeground(subtleStyle),
	)

	return docStyle.Render(barWithOutline)
}

func composeArticle(m *model) string {
	out, _ := glamour.Render(m.currentArticle.body, "dark")
	return out
}

func composeHomePage(m *model) string {
	home := lipgloss.JoinHorizontal(lipgloss.Top,
		bioStyle.Render(m.articleList.View()),
		bioStyle.Width((m.width - 4) / 2).Render(homeBio))
		
	return docStyle.Render(home)
}

// TODO: load article title dynamically and make prettier
func composeStatusBar(m *model) string {
	w := lipgloss.Width

	var statusKey, statusVal string
	
	if m.currentState == ARTICLE {
		statusKey = statusStyle.Render(m.time.Format(time.RFC1123))
		statusVal = statusTextStyle.Width(m.width - w(statusKey)).Render("AN ARTICLE")
	} else {
		statusKey = statusStyle.Render(m.time.Format(time.RFC1123))
		statusVal = statusTextStyle.Width(m.width - w(statusKey)).Render("HOME 🪺")
	} 

	bar := lipgloss.JoinHorizontal(lipgloss.Top,
		statusKey,
		statusVal,
	)

	return statusBarStyle.Width(m.width).Render(bar)
}

func (m model) setViewportSize(width, height int) {
	// defined as constants of 1 -- could be dynamic variables and then ui changes?
	m.articleViewport.Width = width
	m.articleViewport.Height = height - statusBarHeight - helpBarHeight
}

func (m model) setContent(content string) {
	m.articleViewport.SetContent(content)
}

func glamourRender(m model, markdown string) (string, error) {
	width := m.articleViewport.Width

	options := []glamour.TermRendererOption{
		glamour.WithWordWrap(width),
	}

	r, err := glamour.NewTermRenderer(options...)
	if err != nil {
		return "", err
	}

	out, err := r.Render(markdown)
	if err != nil {
		return "", err
	}

	// trim lines
	lines := strings.Split(out, "\n")

	var content strings.Builder
	for i, s := range lines {
		content.WriteString(s)
		if i+1 < len(lines) {
			content.WriteRune('\n')
		}
	}
	return content.String(), nil
}

func renderWithGlamour(m model, md string) tea.Cmd {
	return func() tea.Msg {
		s, err := glamourRender(m, md)
		if err != nil {
			log.Error("error rendering with Glamour", "error", err)
			return errMsg{err}
		}
		return contentRenderedMsg(s)
	}
}

func loadCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func loadArticle(m *model) article {
	path := m.articleList.SelectedItem().FilterValue()
	if path == "" {
		log.Fatal("Missing Data", "Load Article, No Path", path)	
	}
	
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("Loaded Error", "Read File Got", err, "From Path", path)
	}

	return article{
		title: path[9:len(path)-3],
		path: path,
		body: string(data),
		description: "",
	}
}

func (m model) unload() {
	m.articleViewport.SetContent("")
	m.articleViewport.YOffset = 0
}

func (m model) Init() tea.Cmd {
	return loadCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.currentState {
	case LOADING:
		switch msg := msg.(type) {
		case tickMsg:
			if m.loadProgress < 1 {
				// TODO: make funny
				m.loadProgress += .33
				return m, loadCmd()
			} else {
				m.currentState = HOME
				return m, nil
			}
		case timeMsg:
			m.time = time.Time(msg)
			return m, nil
		case tea.WindowSizeMsg:
			m.height = msg.Height
			m.width = (msg.Width - 3)
			return m, nil
		case tea.KeyMsg:
			log.Info("User is mad?", "While Loading Pressed", msg)
			return m, nil
		case contentRenderedMsg:
			log.Warn("Unexpected Msg", "While Loading Recieved", msg)
			return m, nil
		}

	case HOME:
		switch msg := msg.(type) {
		case tickMsg:
			log.Warn("Unexpected Msg", "While Home Received", msg)
			return m, nil
		case timeMsg:
			m.time = time.Time(msg)
			return m, nil
		case tea.WindowSizeMsg:
			m.height = msg.Height
			m.width = (msg.Width - 3)
			m.setViewportSize((msg.Height - 4), (msg.Width - 4))
			return m, nil
		case tea.KeyMsg:
			// TODO: keymap instead?
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "enter":
				m.currentArticle = loadArticle(&m)
				m.currentState = ARTICLE
				return m, renderWithGlamour(m, m.currentArticle.Body())
			default:
				m.articleList, cmd = m.articleList.Update(msg)
				return m, cmd
			}	
		case contentRenderedMsg:
			log.Warn("Unexpected Msg", "While Home Recieved", msg)
			return m, nil
		}
		
	case ARTICLE:
		switch msg := msg.(type) {
		case tickMsg:
			log.Warn("Unexpected Msg", "While Article Received", msg)
			return m, nil
		case timeMsg:
			m.time = time.Time(msg)
			return m, nil
		case tea.WindowSizeMsg:
			m.height = msg.Height
			m.width = (msg.Width - 3)
			m.setViewportSize((msg.Height - 4), (msg.Width - 4))
			return m, renderWithGlamour(m, m.currentArticle.Body())
		case tea.KeyMsg:
			// TODO: keymap instead?
			switch msg.String() {
			case "q", "ctrl+c":
				m.unload()
				m.currentState = HOME
				return m, nil
			default:
				m.articleViewport, cmd = m.articleViewport.Update(msg)
				return m, cmd
			}	
		case contentRenderedMsg:
			m.setContent(string(msg))
			m.articleViewport, cmd = m.articleViewport.Update(msg)
			return m, cmd
			
		}
	default:
		log.Fatal("Update: State OOB", "State", m.currentState)
		return m, tea.Quit
	}
	log.Fatal("Update: Switch Failed", "Last Msg", msg)
	return m, tea.Quit
}

func (m model) View() string {
	printout := strings.Builder{}
	
	switch m.currentState {
	case LOADING:
		printout.WriteString(composeLoadingBar(&m))
		return docStyle.Render(printout.String())
	case HOME:
		printout.WriteString(composeStatusBar(&m))
		printout.WriteString(composeHomePage(&m))
		// TODO: how to generate help bubble for whole page?
		return docStyle.Render(printout.String())
	case ARTICLE:
		// TODO: compose article render here
		printout.WriteString(composeStatusBar(&m)+"\n")
		printout.WriteString(m.articleViewport.View()+"\n")
		//printout.WriteString(composeArticle(&m))
		return docStyle.Render(printout.String())
	}

	log.Fatal("View: State OOB", "State", m.currentState)
	return ""
}

func customMiddleware() wish.Middleware {
	newProgram := func(m tea.Model, opts ...tea.ProgramOption) *tea.Program {
		putty := tea.NewProgram(m, opts...)
		go func() {
			for {
				<-time.After(1 * time.Second)
				putty.Send(timeMsg(time.Now()))
			}
		}()
		return putty
	}
	
	teaHandler := func(session ssh.Session) *tea.Program {
		putty, _, active := session.Pty()
		if !active {
			wish.Fatalln(session, "no active terminal, skipping")
			return nil
		}
		renderer := bubbletea.MakeRenderer(session)

		loadingbar := progress.New(progress.WithScaledGradient("#FF7CCB", "#FDFF8C"))

		var foundArticles []list.Item
		fs.WalkDir(articlesFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Fatal(err)
			}
			if strings.Contains(path, ".md") {
				foundArticles = append(foundArticles,
					article{
						title: path[9:len(path)-3],
						path: path,
						body: "",
						description: "",
					})
			}
			return nil
		})

		ourviewport := viewport.New((putty.Window.Width - 4), (putty.Window.Height - 4))
		ourviewport.YPosition = 0
		ourviewport.Style = articleViewportStyle
		
		articleList := list.New([]list.Item{},
			list.NewDefaultDelegate(),
			(putty.Window.Width / 2),
			(putty.Window.Height / 2))
		articleList.SetItems(foundArticles)
		articleList.Title = "Writing"
		articleList.SetShowStatusBar(false)
		articleList.SetFilteringEnabled(false)
		articleList.Styles.Title = listTitleStyle
		articleList.Styles.PaginationStyle = listPaginationStyle
		articleList.Styles.HelpStyle = listHelpStyle
		articleList.SetShowHelp(false)

		var emptyDoc article
		
		// TODO: Ensure set to loading for 'release'
		// TODO: Set initial state with command line flag/arg?
		// TOOD: Different entry points? i.e. pre-start with chosen article?
		m := model{
			currentState: LOADING,
			width: putty.Window.Width,
			height: putty.Window.Height,
			time:   time.Now(),
			style:  renderer.NewStyle().Foreground(lipgloss.Color("#A8CC8C")),
			loadProgress: 0.0,
			loadBar: loadingbar,
			articleList: articleList,
			articleViewport: ourviewport,
			currentArticle: emptyDoc,
		}
		return newProgram(m, append(bubbletea.MakeOptions(session), tea.WithAltScreen())...)
	}
	return bubbletea.MiddlewareWithProgramHandler(teaHandler, termenv.ANSI256)
}

func main() {
	server, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		wish.WithMiddleware(
			customMiddleware(),
			logging.Middleware(),
		),
	)
	if err != nil {
		log.Error("Could not start server", "error", err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	log.Info("Starting SSH server", "host", host, "port", port)
	go func() {
		if err = server.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			log.Error("Could not start server", "error", err)
			done <- nil
		}
	}()

	<-done
	log.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		log.Error("Could not stop server", "error", err)
	}
}
