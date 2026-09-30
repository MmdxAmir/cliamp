package model

import tea "charm.land/bubbletea/v2"

func (m *Model) handleThemeFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.themePickerCancel()
		return m.quit()
	case "esc":
		m.themePicker.filtering = false
		m.themePicker.filter = ""
		m.themePicker.filtered = nil
		m.themePicker.cursor = m.themePicker.savedCursor
		m.themePicker.scroll = m.themePicker.savedScroll
		return nil
	case "enter":
		m.themePicker.filtering = false
		if m.themePicker.filter == "" {
			m.themePicker.cursor = m.themePicker.savedCursor
			m.themePicker.scroll = m.themePicker.savedScroll
		}
		return nil
	case "down":
		m.themePicker.filtering = false
		if m.themePickerViewCount() > 0 {
			m.themePicker.cursor = 0
			m.themePickerApply()
			m.themePickerMaybeAdjustScroll(m.themePickerVisible())
		}
		return nil
	case "backspace":
		if m.themePicker.filter == "" {
			m.themePicker.filtering = false
			m.themePicker.cursor = m.themePicker.savedCursor
			m.themePicker.scroll = m.themePicker.savedScroll
			return nil
		}
	}

	if m.editText("theme-picker-filter", &m.themePicker.filter, msg) {
		m.themePickerRecomputeFilter()
	}
	return nil
}

// handleThemeKey processes key presses while the theme picker is open.
func (m *Model) handleThemeKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.themePicker.filtering {
		return m.handleThemeFilterKey(msg)
	}

	count := m.themePickerViewCount()
	switch msg.String() {
	case "ctrl+c":
		m.themePickerCancel()
		return m.quit()

	case "up", "k":
		if m.themePicker.cursor > 0 {
			m.themePicker.cursor--
		} else if count > 0 {
			m.themePicker.cursor = count - 1
		}
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "down", "j":
		if m.themePicker.cursor < count-1 {
			m.themePicker.cursor++
		} else if count > 0 {
			m.themePicker.cursor = 0
		}
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "ctrl+x":
		m.toggleExpandedView()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "pgup", "ctrl+u":
		if m.themePicker.cursor > 0 {
			visible := m.themePickerVisible()
			m.themePicker.cursor -= min(m.themePicker.cursor, visible)
			m.themePickerApply()
			m.themePickerMaybeAdjustScroll(visible)
		}

	case "pgdown", "ctrl+d":
		if m.themePicker.cursor < count-1 {
			visible := m.themePickerVisible()
			m.themePicker.cursor = min(count-1, m.themePicker.cursor+visible)
			m.themePickerApply()
			m.themePickerMaybeAdjustScroll(visible)
		}

	case "home", "g":
		m.themePicker.cursor = 0
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "end", "G":
		if count > 0 {
			m.themePicker.cursor = count - 1
		}
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.themePickerVisible())

	case "enter":
		m.themePickerSelect()

	case "/":
		m.themePicker.savedCursor = m.themePicker.cursor
		m.themePicker.savedScroll = m.themePicker.scroll
		m.themePicker.filtering = true
		m.themePicker.filter = ""
		m.themePickerRecomputeFilter()
		return nil

	case "esc", "q", "t":
		m.themePickerCancel()
	}
	return nil
}

func (m *Model) handleVisPickerFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.visPickerCancel()
		return m.quit()
	case "esc":
		m.visPicker.filtering = false
		m.visPicker.filter = ""
		m.visPicker.filtered = nil
		m.visPicker.cursor = m.visPicker.savedCursor
		m.visPicker.scroll = m.visPicker.savedScroll
		return nil
	case "enter":
		m.visPicker.filtering = false
		if m.visPicker.filter == "" {
			m.visPicker.cursor = m.visPicker.savedCursor
			m.visPicker.scroll = m.visPicker.savedScroll
		}
		return nil
	case "down":
		m.visPicker.filtering = false
		if m.visPickerViewCount() > 0 {
			m.visPicker.cursor = 0
			m.visPickerApply()
			m.visPickerMaybeAdjustScroll(m.visPickerVisible())
		}
		return nil
	case "backspace":
		if m.visPicker.filter == "" {
			m.visPicker.filtering = false
			m.visPicker.cursor = m.visPicker.savedCursor
			m.visPicker.scroll = m.visPicker.savedScroll
			return nil
		}
	}

	if m.editText("visualizer-picker-filter", &m.visPicker.filter, msg) {
		m.visPickerRecomputeFilter()
	}
	return nil
}

// handleVisPickerKey processes key presses while the visualizer picker is open.
func (m *Model) handleVisPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.visPicker.filtering {
		return m.handleVisPickerFilterKey(msg)
	}

	count := m.visPickerViewCount()
	switch msg.String() {
	case "ctrl+c":
		m.visPickerCancel()
		return m.quit()

	case "up", "k":
		if m.visPicker.cursor > 0 {
			m.visPicker.cursor--
		} else if count > 0 {
			m.visPicker.cursor = count - 1
		}
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "down", "j":
		if m.visPicker.cursor < count-1 {
			m.visPicker.cursor++
		} else if count > 0 {
			m.visPicker.cursor = 0
		}
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "ctrl+x":
		m.toggleExpandedView()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "pgup", "ctrl+u":
		if m.visPicker.cursor > 0 {
			visible := m.visPickerVisible()
			m.visPicker.cursor -= min(m.visPicker.cursor, visible)
			m.visPickerApply()
			m.visPickerMaybeAdjustScroll(visible)
		}

	case "pgdown", "ctrl+d":
		if m.visPicker.cursor < count-1 {
			visible := m.visPickerVisible()
			m.visPicker.cursor = min(count-1, m.visPicker.cursor+visible)
			m.visPickerApply()
			m.visPickerMaybeAdjustScroll(visible)
		}

	case "home", "g":
		m.visPicker.cursor = 0
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "end", "G":
		if count > 0 {
			m.visPicker.cursor = count - 1
		}
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.visPickerVisible())

	case "enter":
		m.visPickerSelect()

	case "/":
		m.visPicker.savedCursor = m.visPicker.cursor
		m.visPicker.savedScroll = m.visPicker.scroll
		m.visPicker.filtering = true
		m.visPicker.filter = ""
		m.visPickerRecomputeFilter()
		return nil

	case "esc", "q", "ctrl+v":
		m.visPickerCancel()
	}
	return nil
}

func (m *Model) deviceMaybeAdjustScroll(visible int) {
	clampScroll(&m.devicePicker.cursor, &m.devicePicker.scroll, len(m.devicePicker.devices), visible)
}

// handleDeviceKey processes key presses while the audio device picker is open.
func (m *Model) handleDeviceKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.devicePicker.visible = false
		return m.quit()
	case "ctrl+x":
		m.toggleExpandedView()
		m.deviceMaybeAdjustScroll(m.devicePickerVisible())
	case "up", "k":
		if m.devicePicker.cursor > 0 {
			m.devicePicker.cursor--
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = len(m.devicePicker.devices) - 1
		}
		m.deviceMaybeAdjustScroll(m.devicePickerVisible())
	case "down", "j":
		if m.devicePicker.cursor < len(m.devicePicker.devices)-1 {
			m.devicePicker.cursor++
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = 0
		}
		m.deviceMaybeAdjustScroll(m.devicePickerVisible())
	case "enter":
		if len(m.devicePicker.devices) > 0 && m.devicePicker.cursor < len(m.devicePicker.devices) {
			dev := m.devicePicker.devices[m.devicePicker.cursor]
			m.devicePicker.visible = false
			return switchDeviceCmd(dev.Name)
		}
	case "esc", "d":
		m.devicePicker.visible = false
	}
	return nil
}
