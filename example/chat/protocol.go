package main

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/chat"
)

// The vocabulary the two halves share, besides the conversation's, which package chat holds.
type (
	// Chat is the state the chat window renders: the projects, the current project's conversations, and the open
	// conversation.
	Chat struct {
		// Solo says the window shows one conversation alone, popped out of the main window.
		Solo          bool
		Projects      []Project
		Project       int
		Conversations []Conversation
		// Picks counts the conversations and areas picked from the list,
		// so a view that shows one screen at a time, as on a phone, moves
		// to the one picked, though it is the one open already.
		Picks int
		// Conversation is the open conversation.
		chat.Conversation
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
		Transfers []chat.Transfer
		// Selected is the name of the entry to select, such as a file opened from a message.
		Selected string
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

	// Typing is the patch that says who is typing in the open conversation. Who is empty when nobody is.
	Typing struct{ Who string }
)

// Intents, besides the conversation's, which package chat holds.
type (
	ProjectChosen      struct{ Index int }
	ConversationChosen struct{ ID string }
	// AreaChosen travels when the user opens an area of the project, such as "files", and FolderOpened when they
	// open a folder of its files, by path.
	AreaChosen   struct{ Area string }
	FolderOpened struct{ Path string }
	// FilesDropped travels when the user drops files from another program on a folder of the project's files, by
	// the folder's path.
	FilesDropped struct {
		Folder string
		Paths  []string
	}
	// FileShared travels when the user shares a file of the project's in a conversation.
	FileShared struct {
		Folder, Name, Conversation string
	}
	LinkToggled  struct{}
	ThemeToggled struct{}
)

func init() {
	chat.RegisterTypes()
	gunim.RegisterType[Chat]("chat")
	gunim.RegisterType[Typing]("chat.typing")
	gunim.RegisterType[ProjectChosen]("chat.project")
	gunim.RegisterType[ConversationChosen]("chat.conversation.choose")
	gunim.RegisterType[AreaChosen]("chat.area")
	gunim.RegisterType[FolderOpened]("chat.folder")
	gunim.RegisterType[FilesDropped]("chat.files.drop")
	gunim.RegisterType[FileShared]("chat.file.share")
	gunim.RegisterType[LinkToggled]("chat.link")
	gunim.RegisterType[ThemeToggled]("chat.theme")
}
