package chat

import (
	"slices"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// The vocabulary the two halves share.
type (
	// Conversation is one conversation as a [View] shows it: its timeline, the state of the connection, and the
	// message box with what goes with the next message.
	Conversation struct {
		// ID names the conversation: a new one gets a new timeline, which opens at its end, or at the line over
		// the messages not read. Title is its name, and Direct says it is with one person.
		ID, Title string
		Direct    bool
		Items     []Item
		Link      Link
		// Replying is the message the next one replies to, and Editing the ID of the message being edited.
		Replying Quote
		Editing  string
		Draft    Draft
		// Pending are the pictures waiting to go with the next message, and Images every picture the view shows,
		// by ID.
		Pending []Picture
		Images  map[string]*paint.Image
		// Unread is how many messages from others came after the line over the first message not read when the
		// conversation opened, which an [Item] with New set marks; the timeline opens at the line.
		Unread int
		// People are who take part in the conversation, for mentions, and Members how each stands.
		People  []string
		Members []Member
		// Loading says older messages are on their way.
		Loading bool
	}

	// FileRef is a file a message shares, by the folder it is in and its name, with its size.
	FileRef struct {
		Folder, Name string
		Size         int64
	}

	// Transfer is a file on its way up: its name, the folder it goes to, its size and how much of it has gone, how
	// it stands, and why it failed.
	Transfer struct {
		ID, Name, Folder string
		Size, Done       int64
		State            TransferState
		Reason           string
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
		// File is a file the message shares, or has no name.
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
		File                         FileRef
		Private                      bool
	}
	flat := func(m Message) plain {
		return plain{m.ID, m.Author, m.Mine, m.At, m.Body, m.State, m.Edited, m.Withdrawn, m.Continued, m.Reply,
			m.Preview, m.File, m.Private}
	}
	return flat(m) == flat(o)
}

// State is where a message of the user's own is on its way to the server.
type State uint8

// A message is sent, on its way, or turned down.
const (
	Sent State = iota
	Pending
	Failed
)

// Link is the state of the connection to the server.
type Link uint8

// The connection is up, down, or on its way back up.
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
	// Submitted travels when the user sends what the message box holds.
	Submitted struct{ Text string }
	// Drafted travels as the user types in the message box.
	Drafted struct{ Text string }
	// ImagePasted travels when the user pastes a picture, as PNG.
	ImagePasted struct{ PNG []byte }
	// PictureRemoved travels when the user takes a picture waiting to go off the next message.
	PictureRemoved struct{ ID string }
	// ReactionToggled travels when the user adds a reaction to a message, or takes theirs back.
	ReactionToggled struct{ ID, Emoji string }
	// PollVoted travels when the user votes for an option of a poll, or takes their vote back.
	PollVoted struct {
		ID     string
		Option int
	}
	// ReplyAsked travels when the user asks to reply to a message.
	ReplyAsked struct{ ID string }
	// EditAsked travels when the user asks to edit a message of theirs.
	EditAsked struct{ ID string }
	// WithdrawAsked travels when the user asks to withdraw a message of theirs.
	WithdrawAsked struct{ ID string }
	// RetryAsked travels when the user sends a message that failed again.
	RetryAsked struct{ ID string }
	// FileOpened travels when the user opens a file from a message.
	FileOpened struct{ Folder, Name string }
	// TransferRetried travels when the user tries a failed upload again.
	TransferRetried struct{ ID string }
	// TransferDismissed travels when the user takes a failed upload away.
	TransferDismissed struct{ ID string }
	// OlderAsked travels when the timeline comes near its start, for the messages before it.
	OlderAsked struct{}
	// PopOut travels when the user asks for the conversation in a window of its own.
	PopOut struct{}
	// Cancelled travels when the user drops a reply or an edit.
	Cancelled struct{}
)

var registerOnce sync.Once

// RegisterTypes names the package's state and intents for the wire, each under a name starting "chat.". It does
// so once, however often it is called, and [NewView] calls it.
func RegisterTypes() {
	registerOnce.Do(func() {
		gunim.RegisterType[Conversation]("chat.conversation")
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
		gunim.RegisterType[FileOpened]("chat.file.open")
		gunim.RegisterType[TransferRetried]("chat.transfer.retry")
		gunim.RegisterType[TransferDismissed]("chat.transfer.dismiss")
		gunim.RegisterType[Cancelled]("chat.cancel")
		gunim.RegisterType[PopOut]("chat.popout")
		gunim.RegisterType[OlderAsked]("chat.older")
	})
}
