package ktf

import (
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// handleLWCDecoratorMethod serves the static Decorator palette. The values
// derive the requested shade arithmetically from the input color the way the
// reference implementation grades its widget bevels.
func (r *Runtime) handleLWCDecoratorMethod(
	name, descriptor string,
) (uint32, error) {
	scaleColor := func(color uint32, numerator, denominator int32) uint32 {
		result := uint32(0)
		for shift := 0; shift < 24; shift += 8 {
			channel := int32(color>>shift&0xff) * numerator / denominator
			if channel > 0xff {
				channel = 0xff
			}
			result |= uint32(channel) << shift
		}
		return result
	}
	switch name + descriptor {
	case "getColor(I)I":
		index, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		palette := map[uint32]uint32{
			0: 0xc0c0c0, // BKGND_COLOR
			1: 0x000000, // TEXT_COLOR
			2: 0x000080, // FOCUS_COLOR
			3: 0xffffff, // HIGH_LIGHT_COLOR
			4: 0xe0e0e0, // LIGHT_COLOR
			5: 0x808080, // SHADOW_COLOR
			6: 0x404040, // DARK_SHADOW_COLOR
			7: 0x0000ff, // SELECTED_COLOR
			8: 0x000000, // NORMAL_COLOR
		}
		return palette[index], nil
	case "getDarkShadowColor(I)I":
		color, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return scaleColor(color, 1, 4), nil
	case "getShadowColor(I)I":
		color, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return scaleColor(color, 1, 2), nil
	case "getLightColor(I)I":
		color, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return scaleColor(color, 5, 4), nil
	case "getHighLightColor(I)I":
		color, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return scaleColor(color, 3, 2), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleLWCMethod(
	_ context.Context,
	className, name, descriptor string,
	registers []uint32,
) (uint32, error) {
	instance := registers[1]
	state := r.lwcComponent(instance)
	method := name + descriptor

	switch className {
	case "org/kwis/msp/lwc/Component",
		"org/kwis/msp/lwc/ContainerComponent",
		"org/kwis/msp/lwc/TextComponent",
		"org/kwis/msp/lwc/TextBoxComponent":
		if method == "<init>()V" {
			return 0, nil
		}
	case "org/kwis/msp/lwc/ShellComponent":
		switch method {
		case "<init>()V", "<init>(Z)V", "<init>(ZZ)V":
			r.initializeLWCShell(state)
			return 0, nil
		case "<init>(IIII)V", "<init>(IIIIZ)V":
			r.configureLWC(state, registers[2], registers[3], registers[4], registers[5])
			state.shown = false
			return 0, nil
		}
	case "org/kwis/msp/lwc/FormComponent":
		switch method {
		case "<init>()V":
			state.vertical = true
			return 0, nil
		case "<init>(Z)V":
			state.vertical = registers[2] != 0
			return 0, nil
		}
	case "org/kwis/msp/lwc/AnnunciatorComponent":
		switch method {
		case "<init>(Z)V":
			r.initializeLWCAnnunciator(state)
			state.transparent = registers[2] != 0
			return 0, nil
		case "performed(LXTimer;)V":
			return 0, nil
		}
	case "org/kwis/msp/lwc/TextFieldComponent":
		if method == "<init>(Ljava/lang/String;I)V" {
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], true)
			return 0, nil
		}
	case "org/kwis/msp/lwc/LabelComponent":
		switch method {
		case "<init>()V":
			r.initializeLWCTextSize(state, 0, false)
			return 0, nil
		case "<init>(Ljava/lang/String;)V":
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		}
	case "org/kwis/msp/lwc/ProgressComponent":
		if method == "<init>(ZI)V" {
			maximum := int32(registers[3])
			if maximum <= 0 {
				return 0, r.raiseHostJavaException(
					"java/lang/IllegalArgumentException",
				)
			}
			state.progressInput = registers[2] != 0
			state.progressMax = maximum
			state.progressStep = 1
			state.preferredWidth = 100
			state.preferredHeight = 16
			state.width = state.preferredWidth
			state.height = state.preferredHeight
			return 0, nil
		}
	case "org/kwis/msp/lwc/DialogComponent":
		switch method {
		case "<init>(I)V":
			r.initializeLWCShell(state)
			if err := r.setLWCDialogType(state, int32(registers[2])); err != nil {
				return 0, err
			}
			return 0, nil
		case "<init>(Lorg/kwis/msp/lwc/Component;" +
			"Ljava/lang/String;I)V":
			r.initializeLWCShell(state)
			state.work = registers[2]
			state.text = registers[3]
			r.setLWCParent(registers[2], instance)
			if err := r.setLWCDialogType(state, int32(registers[4])); err != nil {
				return 0, err
			}
			return 0, nil
		}
	case "org/kwis/msp/lwc/ButtonComponent":
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(Ljava/lang/String;)V":
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		case "<init>(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;)V":
			state.text = registers[2]
			state.image = registers[3]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		case "<init>(Lorg/kwis/msp/lcdui/Image;)V":
			state.image = registers[2]
			return 0, nil
		}
	case "org/kwis/msp/lwc/CheckboxComponent":
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(Ljava/lang/String;)V":
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		case "<init>(Ljava/lang/String;Lorg/kwis/msp/lwc/CheckboxGroup;)V":
			state.text = registers[2]
			state.group = registers[3]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		}
	case "org/kwis/msp/lwc/CheckboxGroup":
		switch method {
		case "<init>()V":
			return 0, nil
		case "getSelectedCheckbox()Lorg/kwis/msp/lwc/CheckboxComponent;":
			return state.group, nil
		case "select(Lorg/kwis/msp/lwc/CheckboxComponent;)V":
			if previous := state.group; previous != 0 {
				r.lwcComponent(previous).selected = false
			}
			state.group = registers[2]
			if registers[2] != 0 {
				r.lwcComponent(registers[2]).selected = true
			}
			return 0, nil
		}
	case "org/kwis/msp/lwc/ComboComponent",
		"org/kwis/msp/lwc/ListComponent":
		items := r.Vectors[instance]
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(I)V":
			state.mode = int32(registers[2])
			return 0, nil
		case "append(Ljava/lang/String;)I":
			r.Vectors[instance] = append(items, registers[2])
			return uint32(len(items)), nil
		case "append(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;)I":
			r.Vectors[instance] = append(items, registers[2])
			return uint32(len(items)), nil
		case "insert(ILjava/lang/String;)I":
			index := int(int32(registers[2]))
			if index < 0 || index > len(items) {
				return ^uint32(0), nil
			}
			items = append(items, 0)
			copy(items[index+1:], items[index:])
			items[index] = registers[3]
			r.Vectors[instance] = items
			return uint32(index), nil
		case "delete(I)V":
			index := int(int32(registers[2]))
			if index >= 0 && index < len(items) {
				r.Vectors[instance] = append(
					items[:index:index],
					items[index+1:]...,
				)
			}
			return 0, nil
		case "set(ILjava/lang/String;)V":
			index := int(int32(registers[2]))
			if index >= 0 && index < len(items) {
				items[index] = registers[3]
			}
			return 0, nil
		case "getSize()I":
			return uint32(len(items)), nil
		case "select(I)V":
			state.activeIndex = int32(registers[2])
			return 0, nil
		case "getSelectedIndex()I":
			return uint32(state.activeIndex), nil
		case "getString()Ljava/lang/String;":
			index := int(state.activeIndex)
			if index < 0 || index >= len(items) {
				return 0, nil
			}
			return items[index], nil
		case "getImage(I)Lorg/kwis/msp/lcdui/Image;":
			// Item images are not retained by the host list model.
			return 0, nil
		case "controlNumber(Z)V":
			return 0, nil
		}
	case "org/kwis/msp/lwc/Command":
		switch method {
		case "<init>(Ljava/lang/String;)V":
			state.text = registers[2]
			return 0, nil
		case "<init>(Ljava/lang/String;Ljava/lang/Object;)V":
			state.text = registers[2]
			r.lwcEventData[instance] = registers[3]
			return 0, nil
		case "<init>(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;" +
			"Lorg/kwis/msp/lcdui/Image;)V":
			state.text = registers[2]
			state.image = registers[3]
			state.imageActive = registers[4]
			return 0, nil
		case "<init>(Ljava/lang/String;Lorg/kwis/msp/lcdui/Image;" +
			"Lorg/kwis/msp/lcdui/Image;Ljava/lang/Object;)V":
			state.text = registers[2]
			state.image = registers[3]
			state.imageActive = registers[4]
			r.lwcEventData[instance] = registers[5]
			return 0, nil
		case "getString()Ljava/lang/String;":
			return state.text, nil
		case "getNormalImage()Lorg/kwis/msp/lcdui/Image;":
			return state.image, nil
		case "getActiveImage()Lorg/kwis/msp/lcdui/Image;":
			return state.imageActive, nil
		case "getExtObject()Ljava/lang/Object;":
			return r.lwcEventData[instance], nil
		}
	case "org/kwis/msp/lwc/CommandBarComponent":
		commands := r.Vectors[instance]
		switch method {
		case "<init>()V", "<init>(Z)V":
			return 0, nil
		case "addCommand(Lorg/kwis/msp/lwc/Command;)I":
			r.Vectors[instance] = append(commands, registers[2])
			return uint32(len(commands)), nil
		case "getCommand(I)Lorg/kwis/msp/lwc/Command;":
			index := int(int32(registers[2]))
			if index < 0 || index >= len(commands) {
				return 0, nil
			}
			return commands[index], nil
		case "removeCommand(Lorg/kwis/msp/lwc/Command;)V":
			for index, command := range commands {
				if command == registers[2] {
					r.Vectors[instance] = append(
						commands[:index:index],
						commands[index+1:]...,
					)
					break
				}
			}
			return 0, nil
		case "removeAll()V":
			r.Vectors[instance] = nil
			return 0, nil
		case "getSize()I":
			return uint32(len(commands)), nil
		case "setActiveIndex(I)V":
			state.activeIndex = int32(registers[2])
			return 0, nil
		case "getActiveIndex()I":
			return uint32(state.activeIndex), nil
		}
	case "org/kwis/msp/lwc/DateFieldComponent":
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(I)V", "setMode(I)V":
			state.mode = int32(registers[2])
			return 0, nil
		case "getMode()I":
			return uint32(state.mode), nil
		case "setDate(Ljava/util/Date;)V":
			state.date = registers[2]
			return 0, nil
		case "getDate()Ljava/util/Date;":
			if state.date != 0 {
				return state.date, nil
			}
			date, err := r.NewHostJavaObject("java/util/Date")
			if err != nil {
				return 0, err
			}
			r.dates[date] = int64(r.TickMS)
			state.date = date
			return date, nil
		case "getStringValue(I)Ljava/lang/String;":
			moment := time.UnixMilli(r.dates[state.date]).UTC()
			switch int32(registers[2]) {
			case 2: // MODE_TIME
				return r.NewJavaString(moment.Format("15:04"))
			case 3: // MODE_TIME_DATE
				return r.NewJavaString(moment.Format("2006/01/02 15:04"))
			default: // MODE_DATE
				return r.NewJavaString(moment.Format("2006/01/02"))
			}
		case "getTimeZone()Ljava/util/TimeZone;":
			return r.NewHostJavaObject("java/util/TimeZone")
		case "setTimeZone(Ljava/util/TimeZone;)V":
			return 0, nil
		}
	case "org/kwis/msp/lwc/ImageComponent":
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(Lorg/kwis/msp/lcdui/Image;)V":
			state.image = registers[2]
			return 0, nil
		case "<init>(Ljava/lang/String;)V",
			"setImage(Ljava/lang/String;)V":
			resourceName := strings.TrimPrefix(strings.ReplaceAll(
				r.javaStringValue(registers[2]),
				`\`,
				"/",
			), "/")
			if data, ok := r.findKTFResource(resourceName); ok {
				if image, err := r.newJavaEncodedImage(data); err == nil {
					state.image = image
				}
			}
			return 0, nil
		case "play()V", "stop()V":
			return 0, nil
		}
	case "org/kwis/msp/lwc/ScrollbarComponent":
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(I)V", "setDirection(I)V":
			state.mode = int32(registers[2])
			return 0, nil
		case "getDirection()I":
			return uint32(state.mode), nil
		case "setMinimum(I)V":
			state.minimum = int32(registers[2])
			return 0, nil
		case "getMinimum()I":
			return uint32(state.minimum), nil
		case "setMaximum(I)V":
			state.progressMax = int32(registers[2])
			return 0, nil
		case "getMaximum()I":
			return uint32(state.progressMax), nil
		case "setCurrentValue(I)V":
			state.progressValue = int32(registers[2])
			return 0, nil
		case "getCurrentValue()I":
			return uint32(state.progressValue), nil
		case "setViewAmount(I)V":
			state.viewAmount = int32(registers[2])
			return 0, nil
		case "getViewAmount()I":
			return uint32(state.viewAmount), nil
		case "setChangeAmount(I)V":
			state.changeAmount = int32(registers[2])
			return 0, nil
		case "getChangeAmount()I":
			return uint32(state.changeAmount), nil
		case "setForegroundColor(I)V":
			state.foreground = registers[2]
			return 0, nil
		case "getForegroundColor()I":
			return state.foreground, nil
		}
	case "org/kwis/msp/lwc/TickerComponent":
		switch method {
		case "<init>()V":
			return 0, nil
		case "<init>(Ljava/lang/String;)V":
			state.text = registers[2]
			r.initializeLWCTextSize(state, registers[2], false)
			return 0, nil
		case "setDelay(I)V":
			state.delay = int32(registers[2])
			return 0, nil
		case "setTickerState(Z)Z":
			state.selected = registers[2] != 0
			return boolWord(state.selected), nil
		}
	case "org/kwis/msp/lwc/TextComponent$ModeViewer":
		switch method {
		case "<init>()V", "notifyChangeMode()V",
			"paint(Lorg/kwis/msp/lcdui/Graphics;)V":
			return 0, nil
		}
	}

	switch method {
	case "configure(IIIII)V":
		r.configureLWC(
			state,
			registers[2],
			registers[3],
			registers[4],
			registers[5],
		)
		return 0, nil
	case "getX()I":
		return uint32(state.x), nil
	case "getY()I":
		return uint32(state.y), nil
	case "getWidth()I":
		return uint32(state.width), nil
	case "getHeight()I":
		return uint32(state.height), nil
	case "getXOnScreen()I":
		x, _ := r.lwcScreenPosition(instance)
		return uint32(x), nil
	case "getYOnScreen()I":
		_, y := r.lwcScreenPosition(instance)
		return uint32(y), nil
	case "getPreferredWidth()I":
		return uint32(lwcPreferredWidth(state)), nil
	case "getPreferredHeight()I", "getPreferredHeight(I)I":
		return uint32(lwcPreferredHeight(state)), nil
	case "calcPreferredSize(I)V":
		if state.preferredWidth == 0 {
			state.preferredWidth = int32(registers[2])
		}
		if state.preferredHeight == 0 {
			state.preferredHeight = state.height
		}
		return 0, nil
	case "setBackground(I)V":
		state.background = registers[2]
		return 0, nil
	case "getBackground()I":
		return state.background, nil
	case "setForeground(I)V":
		state.foreground = registers[2]
		return 0, nil
	case "getForeground()I":
		return state.foreground, nil
	case "setEventListener(Lorg/kwis/msp/lwc/EventListener;" +
		"Ljava/lang/Object;)V":
		r.listeners[instance] = registers[2]
		r.lwcEventData[instance] = registers[3]
		return 0, nil
	case "setFocus()V":
		r.setLWCFocus(instance, true)
		return 0, nil
	case "setFocus(Lorg/kwis/msp/lwc/Component;)V":
		state.focus = registers[2]
		r.setLWCFocus(registers[2], true)
		return 0, nil
	case "focusNotify(Z)V":
		r.setLWCFocus(instance, registers[2] != 0)
		return 0, nil
	case "hasFocus()Z":
		return boolWord(state.focused), nil
	case "canHandleInput()Z":
		return 1, nil
	case "invalidate()V":
		r.invalidateLWC(instance)
		return 0, nil
	case "isValid()Z":
		return boolWord(state.valid), nil
	case "isShown()Z":
		return boolWord(r.lwcIsShown(instance)), nil
	case "showNotify(Z)V":
		state.shown = registers[2] != 0
		return 0, nil
	case "getCard()Lorg/kwis/msp/lcdui/Card;":
		return r.lwcCard(instance), nil
	case "show()V":
		r.setLWCShown(instance, true)
		r.markLWCRepaint(instance)
		return 0, nil
	case "hide()V":
		r.setLWCShown(instance, false)
		return 0, nil
	case "addComponent(Lorg/kwis/msp/lwc/Component;)I":
		return uint32(r.addLWCChild(instance, len(r.lwcChildren[instance]), registers[2])), nil
	case "addComponent(ILorg/kwis/msp/lwc/Component;)V":
		r.addLWCChild(instance, int(int32(registers[2])), registers[3])
		return 0, nil
	case "setComponent(ILorg/kwis/msp/lwc/Component;)V":
		r.setLWCChild(instance, int(int32(registers[2])), registers[3])
		return 0, nil
	case "getComponent(I)Lorg/kwis/msp/lwc/Component;":
		children := r.lwcChildren[instance]
		index := int(int32(registers[2]))
		if index < 0 || index >= len(children) {
			return 0, nil
		}
		return children[index], nil
	case "getIndexOf(Lorg/kwis/msp/lwc/Component;)I":
		for index, child := range r.lwcChildren[instance] {
			if child == registers[2] {
				return uint32(index), nil
			}
		}
		return ^uint32(0), nil
	case "getNumberOfComponent()I":
		return uint32(len(r.lwcChildren[instance])), nil
	case "removeAllComponents()V":
		r.removeAllLWCChildren(instance)
		return 0, nil
	case "removeComponent(Lorg/kwis/msp/lwc/Component;)V":
		r.removeLWCChildValue(instance, registers[2])
		return 0, nil
	case "removeComponent(I)V":
		r.removeLWCChildIndex(instance, int(int32(registers[2])))
		return 0, nil
	case "validate()V", "layout()V", "layoutChildHorizontal()V",
		"layoutChildVertical()V":
		r.layoutLWC(instance)
		return 0, nil
	case "setPacked(Z)V":
		state.packed = registers[2] != 0
		r.invalidateLWC(instance)
		return 0, nil
	case "getPacked()Z":
		return boolWord(state.packed), nil
	case "setGab(I)V":
		state.gap = int32(registers[2])
		r.invalidateLWC(instance)
		return 0, nil
	case "getGab()I":
		return uint32(state.gap), nil
	case "scrollTo(II)Z":
		for _, child := range r.lwcChildren[instance] {
			childState := r.lwcComponent(child)
			childState.x -= int32(registers[2])
			childState.y -= int32(registers[3])
		}
		return 1, nil
	case "repaint()V", "repaint(IIII)V", "serviceRepaints()V":
		r.markLWCRepaint(instance)
		return 0, nil
	case "setTitle(Lorg/kwis/msp/lwc/Component;)V":
		state.title = registers[2]
		r.setLWCParent(registers[2], instance)
		return 0, nil
	case "getTitle()Lorg/kwis/msp/lwc/Component;":
		return state.title, nil
	case "setCommand(Lorg/kwis/msp/lwc/Component;Z)V":
		state.command = registers[2]
		r.setLWCParent(registers[2], instance)
		return 0, nil
	case "getCommand()Lorg/kwis/msp/lwc/Component;":
		return state.command, nil
	case "setWorkComponent(Lorg/kwis/msp/lwc/Component;)V":
		state.work = registers[2]
		r.setLWCParent(registers[2], instance)
		return 0, nil
	case "getWorkComponent()Lorg/kwis/msp/lwc/Component;":
		return state.work, nil
	case "setMaxLength(I)V":
		r.lwcMaxLengths[instance] = int32(registers[2])
		return 0, nil
	case "getMaxLength()I":
		return uint32(r.lwcMaxLengths[instance]), nil
	case "setString(Ljava/lang/String;)V", "setLabel(Ljava/lang/String;)V":
		state.text = registers[2]
		r.initializeLWCTextSize(
			state,
			registers[2],
			className == "org/kwis/msp/lwc/TextFieldComponent",
		)
		r.invalidateLWC(instance)
		return 0, nil
	case "getString()Ljava/lang/String;", "getLabel()Ljava/lang/String;":
		return state.text, nil
	case "setStep(I)V":
		step := int32(registers[2])
		if step <= 0 || state.progressMax > 0 && step > state.progressMax {
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		state.progressStep = step
		state.progressValue -= state.progressValue % step
		r.invalidateLWC(instance)
		return 0, nil
	case "getStep()I":
		return uint32(max(state.progressStep, 1)), nil
	case "setMargin(II)V":
		state.progressTop = max(int32(registers[2]), 0)
		state.progressBottom = max(int32(registers[3]), 0)
		r.invalidateLWC(instance)
		return 0, nil
	case "setMaxValue(I)V":
		maximum := int32(registers[2])
		if maximum <= 0 {
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		state.progressMax = maximum
		state.progressValue = min(state.progressValue, maximum)
		r.invalidateLWC(instance)
		return 0, nil
	case "getMaxValue()I":
		return uint32(state.progressMax), nil
	case "setValue(I)I":
		value := max(int32(registers[2]), 0)
		value = min(value, state.progressMax)
		step := max(state.progressStep, 1)
		value -= value % step
		state.progressValue = value
		r.invalidateLWC(instance)
		return uint32(value), nil
	case "getValue()I":
		return uint32(state.progressValue), nil
	case "setButtonString(ILjava/lang/String;)V":
		switch int32(registers[2]) {
		case ktfDialogOKButton:
			state.dialogOK = registers[3]
		case ktfDialogCancelButton:
			state.dialogCancel = registers[3]
		default:
			return 0, r.raiseHostJavaException(
				"java/lang/IllegalArgumentException",
			)
		}
		return 0, nil
	case "setType(I)V":
		return 0, r.setLWCDialogType(state, int32(registers[2]))
	case "getType()I":
		return uint32(state.dialogType), nil
	case "setTimeout(I)V":
		state.dialogTimeout = int32(registers[2])
		return 0, nil
	case "getTimeout()I":
		return uint32(state.dialogTimeout), nil
	case "doModal()I":
		r.setLWCShown(instance, true)
		r.markLWCRepaint(instance)
		if state.dialogType == ktfDialogTypeNone {
			state.dialogAction = ktfDialogTimeout
		} else {
			state.dialogAction = ktfDialogOK
		}
		r.setLWCShown(instance, false)
		return uint32(state.dialogAction), nil
	case "getActionState()I":
		return uint32(state.dialogAction), nil
	case "keyNotify(II)Z", "pointerNotify(III)Z",
		"processEvent(IIII)Z":
		if className == "org/kwis/msp/lwc/ProgressComponent" &&
			method == "keyNotify(II)Z" && state.progressInput {
			key := int32(registers[3])
			value := state.progressValue
			switch key {
			case -1, -3:
				value -= max(state.progressStep, 1)
			case -2, -4:
				value += max(state.progressStep, 1)
			default:
				return 0, nil
			}
			state.progressValue = min(max(value, 0), state.progressMax)
			r.invalidateLWC(instance)
			return 1, nil
		}
		return 0, nil
	case "paint(Lorg/kwis/msp/lcdui/Graphics;)V",
		"paintContent(Lorg/kwis/msp/lcdui/Graphics;)V",
		"paintFrame(Lorg/kwis/msp/lcdui/Graphics;)V",
		"controlInset(Z)V", "useFrame(Z)V",
		"setLayout(I)V",
		"setGrabKeyListener(Lorg/kwis/msp/lwc/GrabKeyListener;" +
			"Ljava/lang/Object;)V",
		"grabKey(I)V", "ungrabKey(I)V", "setParameter()V":
		return 0, nil
	case "setFont(Lorg/kwis/msp/lcdui/Font;)V":
		state.font = registers[2]
		return 0, nil
	case "getFont()Lorg/kwis/msp/lcdui/Font;":
		if state.font != 0 {
			return state.font, nil
		}
		return r.ensureDefaultFont()
	case "setImage(Lorg/kwis/msp/lcdui/Image;)V":
		state.image = registers[2]
		r.invalidateLWC(instance)
		return 0, nil
	case "getImage()Lorg/kwis/msp/lcdui/Image;":
		return state.image, nil
	case "setActionListener(Lorg/kwis/msp/lwc/ActionListener;" +
		"Ljava/lang/Object;)V",
		"setChangeListener(Lorg/kwis/msp/lwc/ChangeListener;" +
			"Ljava/lang/Object;)V",
		"setCommandListener(Lorg/kwis/msp/lwc/CommandListener;" +
			"Ljava/lang/Object;)V":
		r.listeners[instance] = registers[2]
		r.lwcEventData[instance] = registers[3]
		return 0, nil
	case "getState()Z":
		return boolWord(state.selected), nil
	case "setState(Z)V":
		selected := registers[2] != 0
		if group := state.group; group != 0 && selected {
			groupState := r.lwcComponent(group)
			if previous := groupState.group; previous != 0 &&
				previous != instance {
				r.lwcComponent(previous).selected = false
			}
			groupState.group = instance
		}
		state.selected = selected
		r.invalidateLWC(instance)
		return 0, nil
	case "getNextTraversalComponent()Lorg/kwis/msp/lwc/Component;",
		"getPrevTraversalComponent()Lorg/kwis/msp/lwc/Component;":
		return 0, nil
	case "getConstraint()I":
		return uint32(state.mode), nil
	case "setConstraint(I)V":
		state.mode = int32(registers[2])
		return 0, nil
	case "insert([CIII)V":
		array := registers[2]
		offset := registers[3]
		count := registers[4]
		index := int(int32(registers[5]))
		inserted, err := r.readJavaCharArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		runes := []rune(r.javaStringValue(state.text))
		if index < 0 || index > len(runes) {
			return 0, r.raiseHostJavaException(
				"java/lang/StringIndexOutOfBoundsException",
			)
		}
		combined := string(runes[:index]) + inserted + string(runes[index:])
		text, err := r.NewJavaString(combined)
		if err != nil {
			return 0, err
		}
		state.text = text
		r.invalidateLWC(instance)
		return 0, nil
	case "delete(II)V":
		index := int(int32(registers[2]))
		count := int(int32(registers[3]))
		runes := []rune(r.javaStringValue(state.text))
		if index < 0 || count < 0 || index+count > len(runes) {
			return 0, r.raiseHostJavaException(
				"java/lang/StringIndexOutOfBoundsException",
			)
		}
		text, err := r.NewJavaString(
			string(runes[:index]) + string(runes[index+count:]),
		)
		if err != nil {
			return 0, err
		}
		state.text = text
		r.invalidateLWC(instance)
		return 0, nil
	}

	signature := className + "." + name + descriptor
	r.UnimplementedJava[signature]++
	r.LastUnimplementedJava = signature
	return 0, nil
}

func (r *Runtime) lwcComponent(instance uint32) *ktfLWCComponent {
	if state := r.lwcComponents[instance]; state != nil {
		return state
	}
	state := &ktfLWCComponent{
		shown:    true,
		vertical: true,
	}
	r.lwcComponents[instance] = state
	if instance == 0 {
		return state
	}
	classAddress, err := r.ReadU32(instance + 4)
	if err != nil {
		return state
	}
	for depth := 0; classAddress != 0 && depth < 32; depth++ {
		class, inspectErr := r.InspectJavaClass(classAddress)
		if inspectErr != nil {
			break
		}
		if class.Name == "org/kwis/msp/lwc/AnnunciatorComponent" {
			r.initializeLWCAnnunciator(state)
			break
		}
		classAddress = class.Parent
	}
	return state
}

func (r *Runtime) initializeLWCShell(state *ktfLWCComponent) {
	if state == nil {
		return
	}
	width, height := int32(240), int32(320)
	if r.frame != nil {
		width = int32(r.frame.Bounds().Dx())
		height = int32(r.frame.Bounds().Dy())
	}
	if state.width == 0 {
		state.width = width
	}
	if state.height == 0 {
		state.height = height
	}
	state.preferredWidth = state.width
	state.preferredHeight = state.height
	state.valid = true
	state.shown = false
}

func (r *Runtime) initializeLWCAnnunciator(state *ktfLWCComponent) {
	r.initializeLWCShell(state)
	state.annunciator = true
	state.x = 0
	state.y = 0
	state.height = ktfAnnunciatorHeight
	state.preferredHeight = ktfAnnunciatorHeight
	state.shown = false
}

func (r *Runtime) setLWCDialogType(
	state *ktfLWCComponent,
	dialogType int32,
) error {
	if state == nil {
		return nil
	}
	switch dialogType {
	case ktfDialogTypeNone:
		state.dialogTimeout = 3_000
	case ktfDialogTypeOK, ktfDialogTypeOKCancel:
		state.dialogTimeout = -1
	default:
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	state.dialogType = dialogType
	state.dialogAction = -2
	return nil
}

func (r *Runtime) initializeLWCTextSize(
	state *ktfLWCComponent,
	text uint32,
	field bool,
) {
	if state == nil {
		return
	}
	width := int32(len([]rune(r.javaStringValue(text)))*8 + 4)
	height := int32(16)
	if field {
		width += 4
		height = 20
	}
	if width < 4 {
		width = 4
	}
	state.preferredWidth = width
	state.preferredHeight = height
	if state.width == 0 {
		state.width = width
	}
	if state.height == 0 {
		state.height = height
	}
}

func (r *Runtime) configureLWC(
	state *ktfLWCComponent,
	x, y, width, height uint32,
) {
	if state == nil {
		return
	}
	state.x = int32(x)
	state.y = int32(y)
	state.width = int32(width)
	state.height = int32(height)
	if state.preferredWidth == 0 {
		state.preferredWidth = state.width
	}
	if state.preferredHeight == 0 {
		state.preferredHeight = state.height
	}
	state.valid = true
}

func lwcPreferredWidth(state *ktfLWCComponent) int32 {
	if state == nil {
		return 0
	}
	if state.preferredWidth > 0 {
		return state.preferredWidth
	}
	return state.width
}

func lwcPreferredHeight(state *ktfLWCComponent) int32 {
	if state == nil {
		return 0
	}
	if state.preferredHeight > 0 {
		return state.preferredHeight
	}
	return state.height
}

func (r *Runtime) lwcScreenPosition(instance uint32) (int32, int32) {
	var x, y int32
	seen := make(map[uint32]bool)
	for depth := 0; instance != 0 && depth < 256; depth++ {
		if seen[instance] {
			break
		}
		seen[instance] = true
		state := r.lwcComponent(instance)
		x += state.x
		y += state.y
		instance = state.Parent
	}
	return x, y
}

func (r *Runtime) setLWCFocus(instance uint32, focused bool) {
	if instance == 0 {
		return
	}
	state := r.lwcComponent(instance)
	state.focused = focused
	if focused && state.Parent != 0 {
		parent := r.lwcComponent(state.Parent)
		if parent.focus != 0 && parent.focus != instance {
			r.lwcComponent(parent.focus).focused = false
		}
		parent.focus = instance
	}
}

func (r *Runtime) invalidateLWC(instance uint32) {
	if instance == 0 {
		return
	}
	state := r.lwcComponent(instance)
	if !state.valid {
		return
	}
	state.valid = false
	r.invalidateLWC(state.Parent)
}

func (r *Runtime) lwcIsShown(instance uint32) bool {
	seen := make(map[uint32]bool)
	for depth := 0; instance != 0 && depth < 256; depth++ {
		if seen[instance] {
			return false
		}
		seen[instance] = true
		state := r.lwcComponent(instance)
		if !state.shown {
			return false
		}
		instance = state.Parent
	}
	return true
}

func (r *Runtime) setLWCShown(instance uint32, shown bool) {
	if instance == 0 {
		return
	}
	r.lwcComponent(instance).shown = shown
	for _, child := range r.lwcChildren[instance] {
		r.setLWCShown(child, shown)
	}
}

func (r *Runtime) lwcCard(instance uint32) uint32 {
	seen := make(map[uint32]bool)
	for depth := 0; instance != 0 && depth < 256; depth++ {
		if seen[instance] {
			return 0
		}
		seen[instance] = true
		state := r.lwcComponent(instance)
		if state.card != 0 {
			return state.card
		}
		instance = state.Parent
	}
	return 0
}

func (r *Runtime) setLWCParent(child, parent uint32) {
	if child == 0 {
		return
	}
	r.lwcComponent(child).Parent = parent
}

func (r *Runtime) addLWCChild(parent uint32, index int, child uint32) int {
	children := r.lwcChildren[parent]
	if index < 0 {
		index = 0
	}
	if index > len(children) {
		index = len(children)
	}
	children = append(children, 0)
	copy(children[index+1:], children[index:])
	children[index] = child
	r.lwcChildren[parent] = children
	r.setLWCParent(child, parent)
	state := r.lwcComponent(parent)
	if state.work == 0 {
		state.work = child
	}
	r.invalidateLWC(parent)
	return index
}

func (r *Runtime) setLWCChild(parent uint32, index int, child uint32) {
	children := r.lwcChildren[parent]
	if index < 0 || index >= len(children) {
		return
	}
	old := children[index]
	if old != 0 {
		r.lwcComponent(old).Parent = 0
	}
	children[index] = child
	r.lwcChildren[parent] = children
	r.setLWCParent(child, parent)
	r.invalidateLWC(parent)
}

func (r *Runtime) removeAllLWCChildren(parent uint32) {
	for _, child := range r.lwcChildren[parent] {
		if child != 0 {
			r.lwcComponent(child).Parent = 0
		}
	}
	delete(r.lwcChildren, parent)
	state := r.lwcComponent(parent)
	state.focus = 0
	state.work = 0
	r.invalidateLWC(parent)
}

func (r *Runtime) removeLWCChildValue(parent, child uint32) {
	for index, candidate := range r.lwcChildren[parent] {
		if candidate == child {
			r.removeLWCChildIndex(parent, index)
			return
		}
	}
}

func (r *Runtime) removeLWCChildIndex(parent uint32, index int) {
	children := r.lwcChildren[parent]
	if index < 0 || index >= len(children) {
		return
	}
	child := children[index]
	copy(children[index:], children[index+1:])
	children = children[:len(children)-1]
	r.lwcChildren[parent] = children
	if child != 0 {
		r.lwcComponent(child).Parent = 0
	}
	state := r.lwcComponent(parent)
	if state.focus == child {
		state.focus = 0
	}
	if state.work == child {
		state.work = 0
	}
	r.invalidateLWC(parent)
}

func (r *Runtime) layoutLWC(instance uint32) {
	state := r.lwcComponent(instance)
	children := r.lwcChildren[instance]
	var cursor int32
	var cross int32
	for _, child := range children {
		childState := r.lwcComponent(child)
		width := lwcPreferredWidth(childState)
		height := lwcPreferredHeight(childState)
		if state.vertical {
			childState.x = 0
			childState.y = cursor
			if state.packed && state.width > 0 {
				width = state.width
			}
			cursor += height + state.gap
			if width > cross {
				cross = width
			}
		} else {
			childState.x = cursor
			childState.y = 0
			if state.packed && state.height > 0 {
				height = state.height
			}
			cursor += width + state.gap
			if height > cross {
				cross = height
			}
		}
		childState.width = width
		childState.height = height
		childState.valid = true
	}
	if len(children) > 0 {
		cursor -= state.gap
	}
	if state.vertical {
		state.preferredWidth = cross
		state.preferredHeight = cursor
	} else {
		state.preferredWidth = cursor
		state.preferredHeight = cross
	}
	state.valid = true
}

func (r *Runtime) markLWCRepaint(instance uint32) {
	if card := r.lwcCard(instance); card != 0 {
		r.dirtyCards[card] = true
	}
}

func boolWord(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func (r *Runtime) javaArrayCopy(
	source uint32,
	sourcePosition uint32,
	target uint32,
	targetPosition uint32,
	count uint32,
) error {
	if source == 0 || target == 0 {
		// Several shipping KTF applications use a null buffer to represent an
		// unavailable optional resource. The handset VM treated that copy as a
		// no-op instead of aborting the application.
		return nil
	}
	if count == 0 {
		// A zero-length copy moves no elements, so the component-type check that
		// would otherwise reject incompatible arrays never runs — the handset VM
		// returns immediately. 훼밀리마트타이쿤 issues arraycopy([B, ..., [Object, 0)
		// at startup and must not fault on the type mismatch.
		return nil
	}
	sourceWords, err := r.ReadWords(source, 2)
	if err != nil {
		return err
	}
	targetWords, err := r.ReadWords(target, 2)
	if err != nil {
		return err
	}
	sourceClass, err := r.InspectJavaClass(sourceWords[1])
	if err != nil {
		return err
	}
	targetClass, err := r.InspectJavaClass(targetWords[1])
	if err != nil {
		return err
	}
	sourceSize, err := ktfJavaArrayElementSize(sourceClass.Name)
	if err != nil {
		return err
	}
	targetSize, err := ktfJavaArrayElementSize(targetClass.Name)
	if err != nil {
		return err
	}
	if sourceSize != targetSize {
		r.tracef(
			"java_arraycopy_type_mismatch:source=0x%08x[%d]:%s:"+
				"target=0x%08x[%d]:%s:count=%d",
			source,
			sourcePosition,
			sourceClass.Name,
			target,
			targetPosition,
			targetClass.Name,
			count,
		)
		return r.raiseHostJavaException("java/lang/ArrayStoreException")
	}
	sourceLength, err := r.ReadU32(sourceWords[0] + 4)
	if err != nil {
		return err
	}
	targetLength, err := r.ReadU32(targetWords[0] + 4)
	if err != nil {
		return err
	}
	if uint64(sourcePosition)+uint64(count) > uint64(sourceLength) ||
		uint64(targetPosition)+uint64(count) > uint64(targetLength) {
		r.tracef(
			"java_arraycopy_bounds:source=0x%08x[%d:%d]/%d:"+
				"target=0x%08x[%d:%d]/%d",
			source,
			sourcePosition,
			uint64(sourcePosition)+uint64(count),
			sourceLength,
			target,
			targetPosition,
			uint64(targetPosition)+uint64(count),
			targetLength,
		)
		return r.raiseHostJavaException(
			"java/lang/ArrayIndexOutOfBoundsException",
		)
	}
	byteCount := uint64(count) * uint64(sourceSize)
	if byteCount > uint64(^uint32(0)) {
		return errors.New("KTF Java arraycopy byte count overflows")
	}
	data := make([]byte, uint32(byteCount))
	sourceAddress := sourceWords[0] + 8 + sourcePosition*sourceSize
	targetAddress := targetWords[0] + 8 + targetPosition*targetSize
	if err := r.CPU.ReadMemory(sourceAddress, data); err != nil {
		return err
	}
	return r.CPU.WriteMemory(targetAddress, data)
}

func (r *Runtime) raiseHostJavaException(name string) error {
	r.snapshotJavaThrow()
	r.rememberJavaThrowName(name)
	_, err := r.raiseJavaException(name, 0)
	return err
}

func ktfJavaArrayElementSize(className string) (uint32, error) {
	if !strings.HasPrefix(className, "[") || len(className) < 2 {
		return 0, fmt.Errorf("KTF Java object %q is not an array", className)
	}
	switch className[1] {
	case 'Z', 'B':
		return 1, nil
	case 'C', 'S':
		return 2, nil
	case 'J', 'D':
		return 8, nil
	default:
		return 4, nil
	}
}

func (r *Runtime) deferCardPaint(
	task *Task,
	card uint32,
	show bool,
) {
	if task == nil || card == 0 {
		return
	}
	if show {
		cards := r.deferredShownCards[task]
		if cards == nil {
			cards = make(map[uint32]bool)
			r.deferredShownCards[task] = cards
		}
		cards[card] = true
	}
	for _, queued := range r.deferredPaintCards[task] {
		if queued == card {
			return
		}
	}
	r.deferredPaintCards[task] = append(r.deferredPaintCards[task], card)
}

func (r *Runtime) releaseDeferredCardPaints(
	ctx context.Context,
	task *Task,
) error {
	cards := r.deferredPaintCards[task]
	shownCards := r.deferredShownCards[task]
	delete(r.deferredPaintCards, task)
	delete(r.deferredShownCards, task)
	for _, card := range cards {
		if shownCards[card] {
			if err := r.notifyCardShown(ctx, card, true); err != nil {
				return err
			}
		}
		if err := r.paintCard(ctx, card); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) notifyCardShown(
	ctx context.Context,
	card uint32,
	shown bool,
) error {
	if card == 0 {
		return nil
	}
	value := uint32(0)
	if shown {
		value = 1
	}
	if r.DeferThreads {
		_, err := r.queueJavaVirtualTask(card, "showNotify", "(Z)V", value)
		return err
	}
	_, err := r.invokeJavaVirtual(ctx, card, "showNotify", "(Z)V", value)
	return err
}

func (r *Runtime) paintCard(ctx context.Context, card uint32) error {
	if card == 0 || !r.dirtyCards[card] {
		return nil
	}
	if task := r.pendingKeyTask(card); task != nil {
		r.deferCardPaint(task, card, false)
		r.tracef("java_paint_defer_key:card=0x%08x", card)
		return nil
	}
	if task := r.PaintTasks[card]; task != nil && !task.Done {
		delete(r.dirtyCards, card)
		if task.WakeAtMS > r.TickMS {
			// The card's paint is mid-flight inside a guest Thread.sleep and
			// virtual time only advances between presentation quanta, so
			// nothing else the guest runs in this quantum can wake it or
			// produce a frame.
			r.PaintStalled = true
		}
		r.tracef("java_paint_coalesce:card=0x%08x", card)
		return nil
	}
	delete(r.PaintTasks, card)
	delete(r.dirtyCards, card)
	graphics, err := r.EnsureScreenGraphics()
	if err != nil {
		return err
	}
	if err := r.applyPendingWIPICScreen(); err != nil {
		return err
	}
	r.ResetScreenGraphics(graphics)
	if r.DeferThreads {
		task, err := r.queueJavaVirtualTask(
			card,
			"paint",
			"(Lorg/kwis/msp/lcdui/Graphics;)V",
			graphics,
		)
		if err != nil {
			return err
		}
		task.presentOnReturn = true
		task.paintCard = card
		task.bestEffortPaint = !r.paintInitializedCards[card]
		if task.bestEffortPaint {
			r.tracef("java_initial_paint_arm:card=0x%08x", card)
		}
		r.PaintTasks[card] = task
		return nil
	}
	if _, err := r.invokeJavaVirtual(
		ctx,
		card,
		"paint",
		"(Lorg/kwis/msp/lcdui/Graphics;)V",
		graphics,
	); err != nil {
		return err
	}
	if err := r.RecordPresentation(); err != nil {
		return err
	}
	r.paintInitializedCards[card] = true
	return nil
}

func (r *Runtime) serviceCardRepaints(
	ctx context.Context,
	card uint32,
) error {
	if card == 0 || !r.dirtyCards[card] {
		return nil
	}
	if task := r.PaintTasks[card]; task != nil && !task.Done {
		task.Done = true
		delete(r.PaintTasks, card)
		r.tracef("java_paint_force_cancel:card=0x%08x", card)
	}
	delete(r.dirtyCards, card)
	graphics, err := r.EnsureScreenGraphics()
	if err != nil {
		return err
	}
	if err := r.applyPendingWIPICScreen(); err != nil {
		return err
	}
	r.ResetScreenGraphics(graphics)
	_, err = r.invokeJavaVirtual(
		ctx,
		card,
		"paint",
		"(Lorg/kwis/msp/lcdui/Graphics;)V",
		graphics,
	)
	if err != nil {
		var unhandled *ktfUnhandledJavaException
		if r.paintInitializedCards[card] || !errors.As(err, &unhandled) {
			return err
		}
		r.tracef(
			"java_initial_paint_discard:%s:card=0x%08x",
			unhandled.name,
			card,
		)
		return nil
	}
	r.paintInitializedCards[card] = true
	return r.RecordPresentation()
}

func (r *Runtime) RecordPresentation() error {
	if state := r.Graphics[r.ScreenGraphics]; r.WipicScreenPending && (state == nil || !state.PixelsDirty) {
		if err := r.applyPendingWIPICScreen(); err != nil {
			return err
		}
	}
	if r.ScreenGraphics != 0 {
		if err := r.syncKTFGraphics(r.ScreenGraphics); err != nil {
			return err
		}
		if serviceID := r.GraphicsServices[r.ScreenGraphics]; serviceID != 0 {
			if r.Services.Graphics.Screen() != serviceID {
				if err := r.Services.Graphics.SetScreen(
					r.ServiceOwner,
					serviceID,
				); err != nil {
					return err
				}
			}
			if _, err := r.Services.Graphics.Present(
				r.ServiceOwner,
				serviceID,
				shared.Rectangle{},
			); err != nil {
				return err
			}
		}
	}
	r.PresentCount++
	r.tracef("java_present:%d", r.PresentCount)
	if r.activeTask != nil {
		// A handset display update is a scheduler boundary. Yield after the
		// host call returns so a paint loop cannot submit many invisible
		// intermediate frames inside one StepFrame.
		r.yieldRequested = true
	}
	return nil
}
