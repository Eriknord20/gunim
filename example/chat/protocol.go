package main

import (
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// The vocabulary the two halves share.
type (
	// Chat is the state the chat view renders: the projects, the current project's conversations, and the open
	// conversation's timeline and message box.
	Chat struct {
		// Solo says the window shows one conversation alone, popped out of the main window.
		Solo          bool
		Projects      []Project
		Project       int
		Conversations []Conversation
		// Current is the open conversation's ID, and Title its name.
		Current string
		// Picks counts the conversations and areas picked from the list,
		// so a view that shows one screen at a time, as on a phone, moves
		// to the one picked, though it is the one open already.
		Picks int
		Title string
		Items []Item
		Link  Link
		// Replying is the message the next one replies to, and Editing the ID of the message being edited.
		Replying Quote
		Editing  string
		Draft    Draft
		// Pending are the pictures waiting to go with the next message, and Images every picture the view shows,
		// by ID.
		Pending []Picture
		Images  map[string]*paint.Image
		// NewKey is the key of the line over the first message not read when the conversation opened, and Unread
		// how many messages from others came after it; the timeline opens at the line.
		NewKey string
		Unread int
		// People are who take part in the open conversation, for mentions, and Members how each stands.
		People  []string
		Members []Member
		// Loading says older messages are on their way.
		Loading bool
		// Area is what the pane shows: the conversation, or "files" for the project's files, which Files holds.
		Area  string
		Files Files
	}

	// Files is a folder of the project's files: its path, its folders' names joined by slashes from the project's
	// top, and what it holds.
	Files struct {
		Path    string
		Entries []FileEntry
		// Transfers are the files on their way up to the project, and those that did not make it.
		Transfers []Transfer
		// Selected is the name of the entry to select, such as a file opened from a message.
		Selected string
	}

	// FileRef is a file of the project's, by the folder it is in and its name, with its size.
	FileRef struct {
		Folder, Name string
		Size         int64
	}

	// Transfer is a file on its way up to the project's files: its name, the folder it goes to, its size and how much
	// of it has gone, how it stands, and why it failed.
	Transfer struct {
		ID, Name, Folder string
		Size, Done       int64
		State            TransferState
		Reason           string
	}

	// FileEntry is a file or a folder: its name, its size in bytes or how many things it holds, when it last changed
	// and who changed it.
	FileEntry struct {
		Name   string
		Folder bool
		Size   int64
		Items  int
		At     time.Time
		By     string
	}

	// Member is a person in a conversation: whether they are active, and a word on how they stand.
	Member struct {
		Name, Status string
		Active       bool
		// You marks the user's own row.
		You bool
	}

	// Picture is a picture in a message, or waiting to go with one: its ID and its size in pixels.
	Picture struct {
		ID   string
		W, H int
	}

	// Project is a project in the rail.
	Project struct {
		Name, Short string
		Unread      int
	}

	// Conversation is a conversation in the sidebar.
	Conversation struct {
		ID, Name string
		Direct   bool
		Unread   int
		// Area is set for a row that opens an area of the project in place of a conversation, such as "files".
		Area string
	}

	// Item is a row of the timeline: a day's heading when Day is set, the line over the messages not yet read when
	// New is set, and a message otherwise.
	Item struct {
		Key string
		Day string
		New bool
		Message
	}

	// Message is a message as the timeline shows it.
	Message struct {
		ID, Author string
		Mine       bool
		At         time.Time
		Body       string
		State      State
		Edited     bool
		Withdrawn  bool
		// Continued says the message follows one by the same author closely enough to share its heading.
		Continued bool
		Reply     Quote
		Pictures  []Picture
		Reactions []Reaction
		Preview   Preview
		Poll      Poll
		// File is a file of the project's the message shares, or has no name.
		File FileRef
		// Private says only the user sees the message, as the answer to a command.
		Private bool
	}

	// Poll is a question in a message, with its options and the votes for them, and how many people voted. A
	// message without a poll has no question.
	Poll struct {
		Question string
		Options  []PollOption
		Voters   int
	}

	// PollOption is one of a poll's options: its text, its votes, whether one is the user's, and who, by name.
	PollOption struct {
		Text  string
		Votes int
		Mine  bool
		Who   string
	}

	// Preview is the card for a link in a message: what the page says of itself, and its picture, by ID. The
	// sender's app fetches it, so neither the server nor the readers ask the page.
	Preview struct {
		URL, Site, Title, Description string
		Picture                       string
	}

	// Reaction is an emoji people reacted to a message with: how many, whether the user is one, and who, by name.
	Reaction struct {
		Emoji string
		Count int
		Mine  bool
		Who   string
	}

	// Quote is the message a reply cites.
	Quote struct {
		ID, Author, Text string
		// Gone says the quoted message was withdrawn.
		Gone bool
	}

	// Draft is text the application puts in the message box. Seq changes each time, so the view sets the text
	// only when the application means to.
	Draft struct {
		Text string
		Seq  int
	}

	// Typing is the patch that says who is typing in the open conversation. Who is empty when nobody is.
	Typing struct{ Who string }
)

// heading reports whether the row is a day's heading or the line over new messages, rather than a message.
func (it Item) heading() bool { return it.Day != "" || it.New }

// same reports whether two rows show the same.
func same(a, b Item) bool {
	return a.Key == b.Key && a.Day == b.Day && a.New == b.New && a.equal(b.Message) && slices.Equal(a.Pictures, b.Pictures) &&
		slices.Equal(a.Reactions, b.Reactions) && a.Poll.Question == b.Poll.Question && a.Poll.Voters == b.Poll.Voters &&
		slices.Equal(a.Poll.Options, b.Poll.Options)
}

// equal compares two messages but for their pictures, which same compares.
func (m Message) equal(o Message) bool {
	type plain struct {
		ID, Author                   string
		Mine                         bool
		At                           time.Time
		Body                         string
		State                        State
		Edited, Withdrawn, Continued bool
		Reply                        Quote
		Preview                      Preview
	}
	flat := func(m Message) plain {
		return plain{m.ID, m.Author, m.Mine, m.At, m.Body, m.State, m.Edited, m.Withdrawn, m.Continued, m.Reply,
			m.Preview}
	}
	return flat(m) == flat(o)
}

// State is where a message of the user's own is on its way to the server.
type State uint8

const (
	Sent State = iota
	Pending
	Failed
)

// Link is the state of the connection to the server.
type Link uint8

const (
	Online Link = iota
	Offline
	Reconnecting
)

// TransferState is how a file on its way up stands.
type TransferState uint8

// A file is encrypted, then sent. While the connection is down it waits, and goes when it is back.
const (
	TransferEncrypting TransferState = iota
	TransferUploading
	TransferWaiting
	TransferUploaded
	TransferFailed
)

// Intents.
type (
	ProjectChosen      struct{ Index int }
	ConversationChosen struct{ ID string }
	// AreaChosen travels when the user opens an area of the project, such as "files", and FolderOpened when they
	// open a folder of its files, by path.
	AreaChosen   struct{ Area string }
	FolderOpened struct{ Path string }
	// FilesDropped travels when the user drops files from another program on a folder of the project's files, by
	// the folder's path. TransferRetried and TransferDismissed travel when they try a failed upload again, or take
	// it away.
	FilesDropped struct {
		Folder string
		Paths  []string
	}
	TransferRetried   struct{ ID string }
	TransferDismissed struct{ ID string }
	// FileShared travels when the user shares a file of the project's in a conversation, and FileOpened when they
	// open one from a message.
	FileShared struct {
		Folder, Name, Conversation string
	}
	FileOpened struct{ Folder, Name string }
	Submitted  struct{ Text string }
	// Drafted travels as the user types in the message box.
	Drafted struct{ Text string }
	// ImagePasted travels when the user pastes a picture, as PNG, and PictureRemoved when they take one waiting
	// off the next message.
	ImagePasted    struct{ PNG []byte }
	PictureRemoved struct{ ID string }
	// ReactionToggled travels when the user adds a reaction to a message, or takes theirs back.
	ReactionToggled struct{ ID, Emoji string }
	// PollVoted travels when the user votes for an option of a poll, or takes their vote back.
	PollVoted struct {
		ID     string
		Option int
	}
	ReplyAsked    struct{ ID string }
	EditAsked     struct{ ID string }
	WithdrawAsked struct{ ID string }
	RetryAsked    struct{ ID string }
	// OlderAsked travels when the timeline comes near its start, for the messages before it.
	OlderAsked struct{}
	// PopOut travels when the user asks for the conversation in a window of its own.
	PopOut struct{}
	// Cancelled travels when the user drops a reply or an edit.
	Cancelled    struct{}
	LinkToggled  struct{}
	ThemeToggled struct{}
)

func init() {
	gunim.RegisterType[Chat]("chat")
	gunim.RegisterType[Typing]("chat.typing")
	gunim.RegisterType[ProjectChosen]("chat.project")
	gunim.RegisterType[ConversationChosen]("chat.conversation")
	gunim.RegisterType[AreaChosen]("chat.area")
	gunim.RegisterType[FolderOpened]("chat.folder")
	gunim.RegisterType[FilesDropped]("chat.files.drop")
	gunim.RegisterType[TransferRetried]("chat.transfer.retry")
	gunim.RegisterType[TransferDismissed]("chat.transfer.dismiss")
	gunim.RegisterType[FileShared]("chat.file.share")
	gunim.RegisterType[FileOpened]("chat.file.open")
	gunim.RegisterType[Submitted]("chat.submit")
	gunim.RegisterType[Drafted]("chat.draft")
	gunim.RegisterType[ImagePasted]("chat.image")
	gunim.RegisterType[PictureRemoved]("chat.image.remove")
	gunim.RegisterType[ReactionToggled]("chat.react")
	gunim.RegisterType[PollVoted]("chat.vote")
	gunim.RegisterType[ReplyAsked]("chat.reply")
	gunim.RegisterType[EditAsked]("chat.edit")
	gunim.RegisterType[WithdrawAsked]("chat.withdraw")
	gunim.RegisterType[RetryAsked]("chat.retry")
	gunim.RegisterType[Cancelled]("chat.cancel")
	gunim.RegisterType[PopOut]("chat.popout")
	gunim.RegisterType[OlderAsked]("chat.older")
	gunim.RegisterType[LinkToggled]("chat.link")
	gunim.RegisterType[ThemeToggled]("chat.theme")
}
