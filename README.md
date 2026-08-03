# whatscli

A command line interface for WhatsApp, based on [go-whatsmeow](https://github.com/tulir/whatsmeow) and [tview](https://github.com/rivo/tview)

![whatscli-screenshot](/doc/screenshot.png?raw=true "WhatsCLI 0.6.5")

> **Note:** this is a customized fork of [normen/whatscli](https://github.com/normen/whatscli) with a reworked look and several UX fixes — see [Changes in this fork](#changes-in-this-fork).

## Changes in this fork

Current version, on top of upstream v1.1.5:

**Visual & theme**
- New default "warm retro" theme (dark slate background, khaki borders, cream body text, teal self messages) defined in `config/theme.go`, selectable via `theme = warm` in the `[general]` config section
- Strict one-color-per-role mapping with new color roles: `list_selected` (selected-chat block), `timestamp` (dim gray), `list_group` (groups now colored differently from accounts), `search_background`
- Sender names in the transcript use a brighter yellow-green so they pop against the contact list

**Chat list & search**
- The Contacts sidebar now only lists chats with actual message activity — the long-dormant `chat_list_mode = recency_only` option is now honored, and the whole address book is no longer dumped into the list on login
- Group members no longer leak into the chat list, and WhatsApp Status / broadcasts / newsletters are never shown or counted as recent chats
- The sidebar is split into collapsible **Contacts** and **Groups** sections, so contacts are never buried under busier groups
- Contact & group search is now instant: fully in-memory instead of hitting the SQLite store and the WhatsApp network API on every keystroke
- The search field has its own background color so it doesn't blend into the app background

**Notifications & presence**
- Desktop notifications (Windows toast) for incoming messages, toggled at runtime with `/notifications` (persisted to the config file)
- Notifications respect WhatsApp's mute setting — muted chats never pop up, and mute changes from your phone sync live
- Online presence for the currently open chat: the status bar shows `online` / `last seen` next to the chat name
- The connection indicator is relabeled to `app: online/offline` so it's clearly about your own connection, not the contact's

**Focus & help**
- All four panels (search, contacts, messages, input) now show a clear focus indicator (sage highlight, bold labels, status-bar segment), for keyboard and mouse focus changes alike
- Fixed the input/search labels disappearing (black-on-black) while the field was focused
- `F1` / `/help` now toggles the help screen instead of wiping the current chat; the chat is restored when closing help

**Config & fixes**
- Fixed stale config files silently overriding theme colors on every start; the loaded config file path is printed at startup

## Features

Things that work.

- Sending and receiving WhatsApp messages in a command line app
- Connects through the Web App API without a browser
- Uses QR code for simple setup
- Allows downloading and opening image/video/audio/document attachments
- Allows sending images, video, audio and documents
- Allows color customization
- Allows basic group management
- Supports desktop notifications
- Binaries for Windows, Mac, Linux and RaspBerry Pi

### Caveats

Heres some things you might expect to work that don't. Plus some other things I should mention.

- Message history depends on what WhatsApp syncs to companion devices and may require `/backlog`
- No automation of messages, no sending of messages through shell commands
- Meta obviously doesn't endorse or like these kinds of apps and they're likely to break when WhatsApp changes stuff in their web app

## Similar Apps

Similar but more features:
- [Nchat](https://github.com/d99kris/nchat)

## Installation

How to get it running and how to use it

### Latest Release

Always fresh, always up to date.

- Download a release
- Put the binary in your PATH (optional)
- Run with `whatscli` (or double-click)
- Scan the QR code with WhatsApp on your phone (resize shell or change font size to see whole code)

### Package Managers

Some ways to install via package managers are supported but the installed version might be out of date.

#### MacOS (homebrew)

- `brew install normen/tap/whatscli`

#### Arch Linux (AUR)

- `https://aur.archlinux.org/packages/whatscli/`

## Usage

Most information, all commands and key bindings are availabe through the in-app help, simply type `/help` and/or `/commands`.

### Login

When starting up, whatscli will immediately try to connect to the WhatsApp server to log in. Keep your phone ready to scan the appearing QR code in WhatsApp on your Phone. If you don't manage to scan the code quick enough just restart the application. If you can not see the whole QR code, reduce the font size of your terminal or increase the window size.

After scanning the QR code the chats should be populated. After you have done this once, whatscli will be able to log into WhatsApp automatically on start. To log out of WhapsApp completely type `/logout`.

### Messaging / Commands

Select a chat on the left and start typing in the input field at the bottom to send messages. Switch between the chat list and the input fiel with `<Tab>`.

For issuing commands the same input field is used. By default commands are prefixed with `/`. You can for example use the `/sendimage /path/to/file.jpg` command to send images, see `/help` for more commands.

When paths are given for commands you don't need to surround the path in quotes, even if it contains spaces. Also don't prefix spaces with backslashes (as the copy-paste function of MacOS does for example).

### Messages

When pressing `Ctrl-w` (default mapping) you enter "message selection mode" which allows selecting a single message and performing operations on them. For example pressing `o` while a message is selected allows opening any attachments through an external application.

#### Image display

You can display images in whatscli using external programs that convert the image to UTF characters. I found that `jp2a` works well for jpeg images, it is available through package managers on most systems. However the "image quality" leaves a lot to be desired. The [PIXterm](https://github.com/eliukblau/pixterm) app allows displaying true-color versions of the images which are quite recognizable already.

To configure the used command and its parameters edit the `show_command` parameter in `whatscli.config`, see `/help` for the config file location.

#### Copy-Pasting User IDs

Some commands such as the `/add` and `/remove` require a "user id" as their input. You can copy the user ID of a selected chat or a selected message to the clipboard with `Ctrl-c` (default mapping) and easily append them to the current input using `Ctrl-v`.

### Notifications

The app supports basic desktop notifications through the `gen2brain/beeep` library, to enable it set `enable_notifications = true` in `whatscli.config`. Set `use_terminal_bell = true` to ring your terminal's bell instead of sending a desktop notification.

### Configuration

Most key bindings, colors and other options can be configured in the `whatscli.config` file, the `/help` command shows its location.

## Development

This app started as my first attempt at writing something in go. Some areas that are marked with `TODO` can still be improved but work mostly. If you want to contribute features or improve the code thats great, send a PR and we can discuss.

### Building

Using a recent version of go, building should be straightforward. Either use `go build`, `go run` etc. or use the included Makefile.

### Structure Overview

The `main.go` contains most UI elements which are based around a tview app running on the main routine. It uses a keymap configuration based on the tslocum/cbind library. Apart from that it mostly manages the selection of messages in the current chat as well as displaying the messages and chat list that the session manager sends.

The `messages/session_manager.go` runs a separate go routine to receive messages from the `go-whatsmeow` library which in turn runs the websocket connection to the WhatsApp server. The session manager receives the messages from `go-whatsmeow` and the commands from the UI via channels that it drains on its main routine. It then updates the UI accordingly using the `UiMessageHandler` interface. This ensures "thread safe" management of the connection and data while both UI and network connection run separately.

Session manager is designed "object like", the MessageDatabase in `messages/storage.go` is similar and somewhat linked to the session manager. In theory the session manager could be run multiple times (multiple accounts) or a different implementation of a session manager could connect to a different service like e.g. Telegram.

In `messages/messages.go` most interfaces and data structures for communication are kept.

The `config/settings.go` keeps a singleton `Config` struct with the config that is loaded via the gopkg.in/ini.v1 library when the app starts. This makes it easy to quickly add new configuration items with default values that can be used across the app.

## License

This software is released under MIT license. Remember that this gives you all freedom except for slapping your name on it.
