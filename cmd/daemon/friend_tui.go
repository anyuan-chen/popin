package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// runFriendsTUI launches the interactive bubbletea program for managing
// friends: accept/deny incoming requests and unfriend existing friends. It has
// two sections (Incoming and Friends) switched via Tab; the cursor (j/k)
// tracks per-section. unfriend prompts a y/n confirmation as it is the one
// destructive, non-undoable action — accept and deny append immediately.
func runFriendsTUI(cfg *daemonConfig, token string) error {
	p := tea.NewProgram(initialFriendModel(cfg, token), tea.WithAltScreen())
	m, err := p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	if fm, ok := m.(friendModel); ok && fm.err != nil {
		return fm.err
	}
	return nil
}

// --- styles ---

var (
	sectionActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	sectionDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	cursorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
	dimStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dangerStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	helpStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

const (
	sectionIncoming = iota
	sectionFriends
)

type friendModel struct {
	cfg   *daemonConfig
	token string

	section   int
	cursor    map[int]int // per-section cursor index
	incoming  []friendUser
	friends   []friendUser
	quit      bool
	err       error
	statusMsg string // transient feedback after an action

	// Unfriend confirmation flow: when set, the next y/n keystroke confirms
	// unfriending the targeted friend.
	confirmTarget *friendUser
}

func initialFriendModel(cfg *daemonConfig, token string) friendModel {
	return friendModel{
		cfg:     cfg,
		token:   token,
		section: sectionIncoming,
		cursor:  map[int]int{sectionIncoming: 0, sectionFriends: 0},
	}
}

// --- tea.Msg types ---

type fetchMsg struct {
	list *friendList
	err  error
}

type actionMsg struct {
	status string
	target string
	err    error
}

// --- Commands ---

func (m friendModel) fetchCmd() tea.Msg {
	list, err := fetchFriends(m.cfg, m.token)
	return fetchMsg{list: list, err: err}
}

func (m friendModel) acceptCmd(target string) tea.Cmd {
	return func() tea.Msg {
		status, err := postFriendAction(m.cfg, m.token, "/api/friends/accept", target)
		return actionMsg{status: status, target: target, err: err}
	}
}

func (m friendModel) denyCmd(target string) tea.Cmd {
	return func() tea.Msg {
		status, err := postFriendAction(m.cfg, m.token, "/api/friends/deny", target)
		return actionMsg{status: status, target: target, err: err}
	}
}

func (m friendModel) unfriendCmd(target string) tea.Cmd {
	return func() tea.Msg {
		status, err := postFriendAction(m.cfg, m.token, "/api/friends/unfriend", target)
		return actionMsg{status: status, target: target, err: err}
	}
}

func fetchCmdFor(m friendModel) tea.Cmd {
	return func() tea.Msg { return m.fetchCmd() }
}

// --- Init/Update/View ---

func (m friendModel) Init() tea.Cmd {
	return fetchCmdFor(m)
}

func (m friendModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil

	case tea.KeyMsg:
		// Confirmation flow intercepts keys before section nav.
		if m.confirmTarget != nil {
			return m.handleConfirm(msg)
		}
		return m.handleKey(msg)

	case fetchMsg:
		if msg.err != nil {
			m.err = msg.err
			m.quit = true
			return m, tea.Quit
		}
		m.incoming = msg.list.Requests
		m.friends = msg.list.Friends
		// Clamp cursors to bounds (lists may have shrunk after an action).
		m.cursor[sectionIncoming] = clamp(m.cursor[sectionIncoming], 0, len(m.incoming)-1)
		m.cursor[sectionFriends] = clamp(m.cursor[sectionFriends], 0, len(m.friends)-1)
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.statusMsg = dangerStyle.Render("error: " + msg.err.Error())
			// A failed action may indicate an expired token; re-fetch to be safe.
			return m, fetchCmdFor(m)
		}
		m.statusMsg = fmt.Sprintf("%s %s", msg.status, msg.target)
		return m, fetchCmdFor(m)
	}

	return m, nil
}

func (m friendModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c", "q":
		m.quit = true
		return m, tea.Quit

	case "tab":
		m.section = 1 - m.section
		m.statusMsg = ""
		return m, nil

	case "up", "k":
		m.cursor[m.section] = clamp(m.cursor[m.section]-1, 0, len(m.currentList())-1)
		m.statusMsg = ""
		return m, nil

	case "down", "j":
		m.cursor[m.section] = clamp(m.cursor[m.section]+1, 0, len(m.currentList())-1)
		m.statusMsg = ""
		return m, nil

	case "a":
		// Accept only meaningful in the Incoming section.
		if m.section != sectionIncoming || len(m.incoming) == 0 {
			return m, nil
		}
		t := m.incoming[m.cursor[sectionIncoming]]
		return m, m.acceptCmd(t.Username)

	case "d":
		// Deny only meaningful in the Incoming section.
		if m.section != sectionIncoming || len(m.incoming) == 0 {
			return m, nil
		}
		t := m.incoming[m.cursor[sectionIncoming]]
		return m, m.denyCmd(t.Username)

	case "u":
		// Unfriend only meaningful in the Friends section, with confirmation.
		if m.section != sectionFriends || len(m.friends) == 0 {
			return m, nil
		}
		t := m.friends[m.cursor[sectionFriends]]
		m.confirmTarget = &t
		m.statusMsg = ""
		return m, nil

	case "r":
		// Manual refresh.
		m.statusMsg = "refreshing..."
		return m, fetchCmdFor(m)
	}

	return m, nil
}

// handleConfirm owns the y/n prompt during an unfriend confirmation. Any other
// key cancels the confirmation and returns to normal navigation.
func (m friendModel) handleConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y", "enter":
		t := *m.confirmTarget
		m.confirmTarget = nil
		return m, m.unfriendCmd(t.Username)
	case "n", "N", "esc":
		m.confirmTarget = nil
		return m, nil
	default:
		// Any other key cancels the confirmation.
		m.confirmTarget = nil
		return m, nil
	}
}

func (m friendModel) currentList() []friendUser {
	if m.section == sectionIncoming {
		return m.incoming
	}
	return m.friends
}

func (m friendModel) View() string {
	var b strings.Builder

	// Header / section tabs.
	incomingLabel := fmt.Sprintf("Incoming (%d)", len(m.incoming))
	friendsLabel := fmt.Sprintf("Friends (%d)", len(m.friends))
	if m.section == sectionIncoming {
		incomingLabel = sectionActiveStyle.Render("▶ " + incomingLabel)
		friendsLabel = sectionDimStyle.Render(friendsLabel)
	} else {
		incomingLabel = sectionDimStyle.Render(incomingLabel)
		friendsLabel = sectionActiveStyle.Render("▶ " + friendsLabel)
	}
	b.WriteString(incomingLabel + "    " + friendsLabel + "\n\n")

	// Current section body.
	if m.section == sectionIncoming {
		if len(m.incoming) == 0 {
			b.WriteString(dimStyle.Render("No pending requests.") + "\n")
		}
		for i, u := range m.incoming {
			prefix := "  "
			if i == m.cursor[sectionIncoming] {
				prefix = cursorStyle.Render("›")
			}
			b.WriteString(prefix + " " + u.Username + "\n")
		}
	} else {
		if len(m.friends) == 0 {
			b.WriteString(dimStyle.Render("No friends yet.") + "\n")
		}
		for i, u := range m.friends {
			prefix := "  "
			if i == m.cursor[sectionFriends] {
				prefix = cursorStyle.Render("›")
			}
			b.WriteString(prefix + " " + u.Username + "\n")
		}
	}

	// Confirmation prompt.
	if m.confirmTarget != nil {
		b.WriteString("\n")
		b.WriteString(dangerStyle.Render(fmt.Sprintf("Remove %s from friends? [y/n]", m.confirmTarget.Username)) + "\n")
	}

	// Transient status.
	if m.statusMsg != "" {
		b.WriteString("\n" + helpStyle.Render(m.statusMsg) + "\n")
	}

	// Help footer.
	b.WriteString("\n")
	help := "tab sections · j/k move · a accept · d deny · u unfriend · r refresh · q quit"
	if m.section == sectionFriends {
		help = "tab sections · j/k move · u unfriend · r refresh · q quit"
	}
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
