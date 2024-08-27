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
	_ "embed"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
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
// TODO: generate article list??
// TODO: embed glow? markdown reader? -> article state
// TODO: extend model for 'current article' period question mark
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
)

const (
	LOADING = 1 + iota
	ARTICLE
	HOME
)

type model struct {
	currState      int
	width          int
	height         int
	spinner        spinner.Model
	time           time.Time
	style          lipgloss.Style
	loadProgress   float64
	loadBar        progress.Model
}

//go:embed homeStatic.txt
var homeStatic string

type timeMsg time.Time
type tickMsg time.Time

// lipgloss style definitions
var (

	// re-used font strengths
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
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
	
	// Home Content
	bioStyle = lipgloss.NewStyle().
			Align(lipgloss.Center).
			Foreground(lipgloss.Color("#FAFAFA")).
			Margin(1, 3, 0, 0).
			Padding(1, 2)

	// Status Bar
	statusNugget = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#343433")).
		Background(lipgloss.Color("#AF9CA2"))

	statusStyle = lipgloss.NewStyle().
			Inherit(statusBarStyle).
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#8D6888")).
			Padding(0, 1).
			MarginRight(1)

	statusText = lipgloss.NewStyle().Inherit(statusBarStyle)
	
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
		lipgloss.WithWhitespaceForeground(subtle),
	)

	return docStyle.Render(barWithOutline)
}

// TODO: Setup two columns, one with bio text and other with article list
func composeHomePage(m *model) string {
	return bioStyle.Width(m.width / 2).Render(homeStatic)
}

// TODO: swap status chit and time text, (align small chit right), load article text in white at top
func composeStatusBar(m *model) string {
	w := lipgloss.Width
	
	// TODO: can we load the title of the current page instead of "status"?
	statusKey := statusStyle.Render("STATUS")
	statusVal := statusText.
		Width(m.width - w(statusKey)).
		Render(m.time.Format(time.RFC1123))

	// TODO: missing right side padding??
	bar := lipgloss.JoinHorizontal(lipgloss.Top,
		statusKey,
		statusVal,
	)

	return statusBarStyle.Width(m.width).Render(bar)
}

func loadCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	return loadCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.loadProgress < 1.05 {
			m.loadProgress += .35
			return m, loadCmd()
		} else {
			m.currState = HOME
			return m, nil
		}
	case timeMsg:
		m.time = time.Time(msg)
		return m, nil
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			return m, nil
		}
	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func (m model) View() string {
	printout := strings.Builder{}

	switch m.currState {
	case LOADING:
		printout.WriteString(composeLoadingBar(&m))
		return docStyle.Render(printout.String())
	case HOME:
		printout.WriteString(composeStatusBar(&m))
		printout.WriteString(composeHomePage(&m))
		// TODO: generate help bubble
		return docStyle.Render(printout.String())
	}

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

		spin := spinner.New()
		spin.Spinner = spinner.Dot
		spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
		loadingbar := progress.New(progress.WithScaledGradient("#FF7CCB", "#FDFF8C"))

		// TODO: Ensure set to loading for 'release'
		m := model{
			currState: HOME,
			width: putty.Window.Width,
			height: putty.Window.Height,
			spinner: spin,
			time:   time.Now(),
			style:  renderer.NewStyle().Foreground(lipgloss.Color("#A8CC8C")),
			loadProgress: 0.0,
			loadBar: loadingbar,
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
