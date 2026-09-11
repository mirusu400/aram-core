package skvm

import (
	"context"
	"fmt"
)

const (
	gameXField          = "$game.x"
	gameYField          = "$game.y"
	gameWidthField      = "$game.width"
	gameHeightField     = "$game.height"
	gameVisibleField    = "$game.visible"
	gameImageField      = "$game.image"
	gameFrameWidth      = "$game.frameWidth"
	gameFrameHeight     = "$game.frameHeight"
	gameFrameField      = "$game.frame"
	gameSequenceField   = "$game.sequence"
	gameTransformField  = "$game.transform"
	gameRefXField       = "$game.referenceX"
	gameRefYField       = "$game.referenceY"
	gameCollisionX      = "$game.collisionX"
	gameCollisionY      = "$game.collisionY"
	gameCollisionWidth  = "$game.collisionWidth"
	gameCollisionHeight = "$game.collisionHeight"
	gameColumnsField    = "$game.columns"
	gameRowsField       = "$game.rows"
	gameCellsField      = "$game.cells"
	gameAnimatedField   = "$game.animated"
	gameLayersField     = "$game.layers"
	gameViewXField      = "$game.viewX"
	gameViewYField      = "$game.viewY"
	gameViewWidthField  = "$game.viewWidth"
	gameViewHeightField = "$game.viewHeight"
	gameCanvasImage     = "$game.canvasImage"
	gameCanvasGraphics  = "$game.canvasGraphics"
)

func (vm *VM) installMIDPGameNatives() {
	vm.installLayerNatives()
	vm.installSpriteNatives()
	vm.installTiledLayerNatives()
	vm.installLayerManagerNatives()
	vm.installGameCanvasNatives()
	for name, value := range map[string]int32{
		"UP_PRESSED": 0x0002, "LEFT_PRESSED": 0x0004, "RIGHT_PRESSED": 0x0020,
		"DOWN_PRESSED": 0x0040, "FIRE_PRESSED": 0x0100, "GAME_A_PRESSED": 0x0200,
		"GAME_B_PRESSED": 0x0400, "GAME_C_PRESSED": 0x0800, "GAME_D_PRESSED": 0x1000,
	} {
		vm.RegisterStaticField("javax/microedition/lcdui/game/GameCanvas", name, "I", IntValue(value))
	}
}

func (vm *VM) initializeLayer(receiver uint32, width, height int32) error {
	if width < 0 || height < 0 {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	for field, value := range map[string]int32{
		gameXField: 0, gameYField: 0, gameWidthField: width,
		gameHeightField: height, gameVisibleField: 1,
	} {
		_ = setObjectField(vm, receiver, field, IntValue(value))
	}
	return nil
}

func (vm *VM) installLayerNatives() {
	for _, method := range []struct{ name, field string }{
		{"getX", gameXField}, {"getY", gameYField}, {"getWidth", gameWidthField}, {"getHeight", gameHeightField},
	} {
		method := method
		vm.registerIntGetter("javax/microedition/lcdui/game/Layer", method.name, method.field, 0)
	}
	vm.RegisterNative("javax/microedition/lcdui/game/Layer", "isVisible", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Layer")
		}
		if value, ok := object.Fields[gameVisibleField]; ok {
			return value, true, nil
		}
		return IntValue(1), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Layer", "setPosition", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		x, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		y, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		_ = setObjectField(vm, receiver, gameXField, IntValue(x))
		return Value{}, false, setObjectField(vm, receiver, gameYField, IntValue(y))
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Layer", "move", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		dx, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		dy, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		x, _ := vm.gameInt(receiver, gameXField)
		y, _ := vm.gameInt(receiver, gameYField)
		_ = setObjectField(vm, receiver, gameXField, IntValue(x+dx))
		return Value{}, false, setObjectField(vm, receiver, gameYField, IntValue(y+dy))
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Layer", "setVisible", "(Z)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return Value{}, false, setObjectField(vm, receiver, gameVisibleField, args[0])
	})
}

func (vm *VM) gameInt(receiver uint32, field string) (int32, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return 0, fmt.Errorf("invalid game object")
	}
	value, ok := object.Fields[field]
	if !ok {
		return 0, nil
	}
	return value.Int()
}

func (vm *VM) installSpriteNatives() {
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "<init>", "(Ljavax/microedition/lcdui/Image;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		imageReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		image, err := vm.image(imageReference)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		return Value{}, false, vm.initializeSprite(receiver, imageReference, int32(image.width), int32(image.height))
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "<init>", "(Ljavax/microedition/lcdui/Image;II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		imageReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		width, _ := intArgument(args, 1)
		height, _ := intArgument(args, 2)
		return Value{}, false, vm.initializeSprite(receiver, imageReference, width, height)
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "<init>", "(Ljavax/microedition/lcdui/game/Sprite;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		sourceReference, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		source, ok := vm.Object(sourceReference)
		destination, destinationOK := vm.Object(receiver)
		if !ok || !destinationOK || source.Class != "javax/microedition/lcdui/game/Sprite" {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		for field, value := range source.Fields {
			if field == gameSequenceField {
				sequenceReference, _ := value.Reference()
				sequenceObject, _ := vm.Object(sequenceReference)
				destination.Fields[field] = ReferenceValue(vm.newArray("[I", append([]Value(nil), sequenceObject.Array.Elements...)))
				continue
			}
			destination.Fields[field] = value
		}
		return Value{}, false, nil
	})
	for _, method := range []struct{ name, field string }{
		{"getFrame", gameFrameField},
	} {
		method := method
		vm.registerIntGetter("javax/microedition/lcdui/game/Sprite", method.name, method.field, 0)
	}
	for _, method := range []struct {
		name string
		x    bool
	}{{"getRefPixelX", true}, {"getRefPixelY", false}} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/game/Sprite", method.name, "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			x, y := vm.spriteReferencePosition(receiver, 0, 0)
			if method.x {
				return IntValue(x), true, nil
			}
			return IntValue(y), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "getFrameSequenceLength", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		sequence, err := vm.objectArray(receiver, gameSequenceField, "[I")
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(len(sequence.Elements))), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "getRawFrameCount", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		count, err := vm.spriteRawFrameCount(receiver)
		return IntValue(count), true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "setFrame", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		frame, _ := intArgument(args, 0)
		sequence, _ := vm.objectArray(receiver, gameSequenceField, "[I")
		if frame < 0 || int(frame) >= len(sequence.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		return Value{}, false, setObjectField(vm, receiver, gameFrameField, IntValue(frame))
	})
	for _, method := range []struct {
		name  string
		delta int32
	}{{"nextFrame", 1}, {"prevFrame", -1}} {
		method := method
		vm.RegisterNative("javax/microedition/lcdui/game/Sprite", method.name, "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			frame, _ := vm.gameInt(receiver, gameFrameField)
			sequence, _ := vm.objectArray(receiver, gameSequenceField, "[I")
			frame = (frame + method.delta + int32(len(sequence.Elements))) % int32(len(sequence.Elements))
			return Value{}, false, setObjectField(vm, receiver, gameFrameField, IntValue(frame))
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "setFrameSequence", "([I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		sequenceReference, _ := referenceArgument(args, 0)
		if sequenceReference == 0 {
			count, err := vm.spriteRawFrameCount(receiver)
			if err != nil {
				return Value{}, false, err
			}
			values := make([]Value, count)
			for index := range values {
				values[index] = IntValue(int32(index))
			}
			_ = setObjectField(vm, receiver, gameSequenceField, ReferenceValue(vm.newArray("[I", values)))
			_ = setObjectField(vm, receiver, gameFrameField, IntValue(0))
			return Value{}, false, nil
		}
		object, ok := vm.Object(sequenceReference)
		if !ok || object.Array == nil || len(object.Array.Elements) == 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		count, _ := vm.spriteRawFrameCount(receiver)
		for _, value := range object.Array.Elements {
			frame, valueErr := value.Int()
			if valueErr != nil || frame < 0 || frame >= count {
				return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
			}
		}
		copyReference := vm.newArray("[I", append([]Value(nil), object.Array.Elements...))
		_ = setObjectField(vm, receiver, gameSequenceField, ReferenceValue(copyReference))
		_ = setObjectField(vm, receiver, gameFrameField, IntValue(0))
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "setImage", "(Ljavax/microedition/lcdui/Image;II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		imageReference, _ := referenceArgument(args, 0)
		width, _ := intArgument(args, 1)
		height, _ := intArgument(args, 2)
		x, _ := vm.gameInt(receiver, gameXField)
		y, _ := vm.gameInt(receiver, gameYField)
		if err := vm.initializeSprite(receiver, imageReference, width, height); err != nil {
			return Value{}, false, err
		}
		_ = setObjectField(vm, receiver, gameXField, IntValue(x))
		_ = setObjectField(vm, receiver, gameYField, IntValue(y))
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "defineReferencePixel", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		oldX, _ := vm.gameInt(receiver, gameRefXField)
		oldY, _ := vm.gameInt(receiver, gameRefYField)
		newX, _ := intArgument(args, 0)
		newY, _ := intArgument(args, 1)
		worldX, worldY := vm.spriteReferencePosition(receiver, oldX, oldY)
		_ = setObjectField(vm, receiver, "$game.referenceLocalX", IntValue(newX))
		_ = setObjectField(vm, receiver, "$game.referenceLocalY", IntValue(newY))
		return Value{}, false, vm.positionSpriteReference(receiver, worldX, worldY)
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "setRefPixelPosition", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		x, _ := intArgument(args, 0)
		y, _ := intArgument(args, 1)
		return Value{}, false, vm.positionSpriteReference(receiver, x, y)
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "setTransform", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		transform, _ := intArgument(args, 0)
		if transform < transNone || transform > transMirrorRot90 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		worldX, worldY := vm.spriteReferencePosition(receiver, 0, 0)
		_ = setObjectField(vm, receiver, gameTransformField, IntValue(transform))
		width, _ := vm.gameInt(receiver, gameFrameWidth)
		height, _ := vm.gameInt(receiver, gameFrameHeight)
		if midpRegionQuarterTurn(transform) {
			width, height = height, width
		}
		_ = setObjectField(vm, receiver, gameWidthField, IntValue(width))
		_ = setObjectField(vm, receiver, gameHeightField, IntValue(height))
		return Value{}, false, vm.positionSpriteReference(receiver, worldX, worldY)
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "defineCollisionRectangle", "(IIII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		for index, field := range []string{gameCollisionX, gameCollisionY, gameCollisionWidth, gameCollisionHeight} {
			value, _ := intArgument(args, index)
			if index >= 2 && value < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			_ = setObjectField(vm, receiver, field, IntValue(value))
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "paint", "(Ljavax/microedition/lcdui/Graphics;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return vm.paintSprite(receiver, args)
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "collidesWith", "(Ljavax/microedition/lcdui/game/Sprite;Z)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		other, _ := referenceArgument(args, 0)
		pixel, _ := intArgument(args, 1)
		collision, err := vm.spriteSpriteCollision(receiver, other, pixel != 0)
		return IntValue(boolInt(collision)), true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "collidesWith", "(Ljavax/microedition/lcdui/Image;IIZ)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		imageReference, _ := referenceArgument(args, 0)
		x, _ := intArgument(args, 1)
		y, _ := intArgument(args, 2)
		pixel, _ := intArgument(args, 3)
		collision, err := vm.spriteImageCollision(receiver, imageReference, x, y, pixel != 0)
		return IntValue(boolInt(collision)), true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/game/Sprite", "collidesWith", "(Ljavax/microedition/lcdui/game/TiledLayer;Z)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		tiled, _ := referenceArgument(args, 0)
		pixel, _ := intArgument(args, 1)
		collision, err := vm.spriteTiledCollision(receiver, tiled, pixel != 0)
		return IntValue(boolInt(collision)), true, err
	})
	for name, value := range map[string]int32{"TRANS_NONE": 0, "TRANS_MIRROR_ROT180": 1, "TRANS_MIRROR": 2, "TRANS_ROT180": 3, "TRANS_MIRROR_ROT270": 4, "TRANS_ROT90": 5, "TRANS_ROT270": 6, "TRANS_MIRROR_ROT90": 7} {
		vm.RegisterStaticField("javax/microedition/lcdui/game/Sprite", name, "I", IntValue(value))
	}
}

func (vm *VM) initializeSprite(receiver, imageReference uint32, width, height int32) error {
	image, err := vm.image(imageReference)
	if err != nil {
		return vm.newThrowable("java/lang/NullPointerException", "")
	}
	if width <= 0 || height <= 0 || int32(image.width)%width != 0 || int32(image.height)%height != 0 {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	if err := vm.initializeLayer(receiver, width, height); err != nil {
		return err
	}
	count := int32(image.width) / width * (int32(image.height) / height)
	sequence := make([]Value, count)
	for index := range sequence {
		sequence[index] = IntValue(int32(index))
	}
	for field, value := range map[string]Value{
		gameImageField: ReferenceValue(imageReference), gameFrameWidth: IntValue(width), gameFrameHeight: IntValue(height),
		gameFrameField: IntValue(0), gameSequenceField: ReferenceValue(vm.newArray("[I", sequence)), gameTransformField: IntValue(0),
		gameRefXField: IntValue(0), gameRefYField: IntValue(0), "$game.referenceLocalX": IntValue(0), "$game.referenceLocalY": IntValue(0),
		gameCollisionX: IntValue(0), gameCollisionY: IntValue(0), gameCollisionWidth: IntValue(width), gameCollisionHeight: IntValue(height),
	} {
		_ = setObjectField(vm, receiver, field, value)
	}
	return nil
}

func (vm *VM) spriteRawFrameCount(receiver uint32) (int32, error) {
	imageValue, err := objectField(vm, receiver, gameImageField)
	if err != nil {
		return 0, err
	}
	imageReference, _ := imageValue.Reference()
	image, err := vm.image(imageReference)
	if err != nil {
		return 0, err
	}
	width, _ := vm.gameInt(receiver, gameFrameWidth)
	height, _ := vm.gameInt(receiver, gameFrameHeight)
	return int32(image.width) / width * (int32(image.height) / height), nil
}

func (vm *VM) spriteReferencePosition(receiver uint32, _, _ int32) (int32, int32) {
	x, _ := vm.gameInt(receiver, gameXField)
	y, _ := vm.gameInt(receiver, gameYField)
	localX, _ := vm.gameInt(receiver, "$game.referenceLocalX")
	localY, _ := vm.gameInt(receiver, "$game.referenceLocalY")
	transform, _ := vm.gameInt(receiver, gameTransformField)
	width, _ := vm.gameInt(receiver, gameFrameWidth)
	height, _ := vm.gameInt(receiver, gameFrameHeight)
	transformedX, transformedY := midpReferenceTransform(transform, localX, localY, width, height)
	return x + transformedX, y + transformedY
}

func midpReferenceTransform(transform, x, y, width, height int32) (int32, int32) {
	switch transform {
	case transMirror:
		return width - 1 - x, y
	case transRot180:
		return width - 1 - x, height - 1 - y
	case transMirrorRot180:
		return x, height - 1 - y
	case transRot90:
		return height - 1 - y, x
	case transRot270:
		return y, width - 1 - x
	case transMirrorRot90:
		return height - 1 - y, width - 1 - x
	case transMirrorRot270:
		return y, x
	default:
		return x, y
	}
}

func (vm *VM) positionSpriteReference(receiver uint32, worldX, worldY int32) error {
	localX, _ := vm.gameInt(receiver, "$game.referenceLocalX")
	localY, _ := vm.gameInt(receiver, "$game.referenceLocalY")
	transform, _ := vm.gameInt(receiver, gameTransformField)
	width, _ := vm.gameInt(receiver, gameFrameWidth)
	height, _ := vm.gameInt(receiver, gameFrameHeight)
	x, y := midpReferenceTransform(transform, localX, localY, width, height)
	_ = setObjectField(vm, receiver, gameXField, IntValue(worldX-x))
	_ = setObjectField(vm, receiver, gameYField, IntValue(worldY-y))
	_ = setObjectField(vm, receiver, gameRefXField, IntValue(worldX))
	return setObjectField(vm, receiver, gameRefYField, IntValue(worldY))
}

func (vm *VM) spriteSource(receiver uint32) (*imageState, int32, int32, int32, int32, int32, error) {
	imageValue, err := objectField(vm, receiver, gameImageField)
	if err != nil {
		return nil, 0, 0, 0, 0, 0, err
	}
	imageReference, _ := imageValue.Reference()
	image, err := vm.image(imageReference)
	if err != nil {
		return nil, 0, 0, 0, 0, 0, err
	}
	width, _ := vm.gameInt(receiver, gameFrameWidth)
	height, _ := vm.gameInt(receiver, gameFrameHeight)
	frame, _ := vm.gameInt(receiver, gameFrameField)
	sequence, _ := vm.objectArray(receiver, gameSequenceField, "[I")
	raw, _ := sequence.Elements[frame].Int()
	columns := int32(image.width) / width
	return image, (raw % columns) * width, (raw / columns) * height, width, height, raw, nil
}

func (vm *VM) paintSprite(receiver uint32, args []Value) (Value, bool, error) {
	visible, _ := vm.gameInt(receiver, gameVisibleField)
	if visible == 0 {
		return Value{}, false, nil
	}
	graphicsReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	imageValue, _ := objectField(vm, receiver, gameImageField)
	imageReference, _ := imageValue.Reference()
	_, sourceX, sourceY, width, height, _, err := vm.spriteSource(receiver)
	if err != nil {
		return Value{}, false, err
	}
	transform, _ := vm.gameInt(receiver, gameTransformField)
	x, _ := vm.gameInt(receiver, gameXField)
	y, _ := vm.gameInt(receiver, gameYField)
	return nativeDrawRegion(context.Background(), vm, graphicsReference, []Value{ReferenceValue(imageReference), IntValue(sourceX), IntValue(sourceY), IntValue(width), IntValue(height), IntValue(transform), IntValue(x), IntValue(y), IntValue(0)})
}

type gameRect struct{ x, y, width, height int32 }

func intersectGameRects(a, b gameRect) (gameRect, bool) {
	x := max(a.x, b.x)
	y := max(a.y, b.y)
	right := min(a.x+a.width, b.x+b.width)
	bottom := min(a.y+a.height, b.y+b.height)
	return gameRect{x, y, right - x, bottom - y}, right > x && bottom > y
}

func (vm *VM) spriteCollisionRect(receiver uint32) gameRect {
	x, _ := vm.gameInt(receiver, gameXField)
	y, _ := vm.gameInt(receiver, gameYField)
	cx, _ := vm.gameInt(receiver, gameCollisionX)
	cy, _ := vm.gameInt(receiver, gameCollisionY)
	cw, _ := vm.gameInt(receiver, gameCollisionWidth)
	ch, _ := vm.gameInt(receiver, gameCollisionHeight)
	return gameRect{x + cx, y + cy, cw, ch}
}

func (vm *VM) spriteWorldPixel(receiver uint32, worldX, worldY int32) (bool, error) {
	image, sourceX, sourceY, width, height, _, err := vm.spriteSource(receiver)
	if err != nil {
		return false, err
	}
	x, _ := vm.gameInt(receiver, gameXField)
	y, _ := vm.gameInt(receiver, gameYField)
	transform, _ := vm.gameInt(receiver, gameTransformField)
	u, v := worldX-x, worldY-y
	drawnWidth, drawnHeight := width, height
	if midpRegionQuarterTurn(transform) {
		drawnWidth, drawnHeight = height, width
	}
	if u < 0 || v < 0 || u >= drawnWidth || v >= drawnHeight {
		return false, nil
	}
	sx, sy := midpRegionSource(transform, u, v, width, height)
	color, err := vm.services.Graphics.Pixel(vm.serviceOwner, image.surface, sourceX+sx, sourceY+sy)
	return color.A != 0, err
}

func (vm *VM) spriteSpriteCollision(first, second uint32, pixel bool) (bool, error) {
	if second == 0 {
		return false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	firstVisible, _ := vm.gameInt(first, gameVisibleField)
	secondVisible, _ := vm.gameInt(second, gameVisibleField)
	if firstVisible == 0 || secondVisible == 0 {
		return false, nil
	}
	intersection, ok := intersectGameRects(vm.spriteCollisionRect(first), vm.spriteCollisionRect(second))
	if !ok || !pixel {
		return ok, nil
	}
	for y := intersection.y; y < intersection.y+intersection.height; y++ {
		for x := intersection.x; x < intersection.x+intersection.width; x++ {
			firstOpaque, err := vm.spriteWorldPixel(first, x, y)
			if err != nil {
				return false, err
			}
			if !firstOpaque {
				continue
			}
			secondOpaque, err := vm.spriteWorldPixel(second, x, y)
			if err != nil {
				return false, err
			}
			if secondOpaque {
				return true, nil
			}
		}
	}
	return false, nil
}

func (vm *VM) spriteImageCollision(sprite, imageReference uint32, imageX, imageY int32, pixel bool) (bool, error) {
	image, err := vm.image(imageReference)
	if err != nil {
		return false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	visible, _ := vm.gameInt(sprite, gameVisibleField)
	if visible == 0 {
		return false, nil
	}
	intersection, ok := intersectGameRects(vm.spriteCollisionRect(sprite), gameRect{imageX, imageY, int32(image.width), int32(image.height)})
	if !ok || !pixel {
		return ok, nil
	}
	for y := intersection.y; y < intersection.y+intersection.height; y++ {
		for x := intersection.x; x < intersection.x+intersection.width; x++ {
			opaque, pixelErr := vm.spriteWorldPixel(sprite, x, y)
			if pixelErr != nil {
				return false, pixelErr
			}
			if !opaque {
				continue
			}
			color, pixelErr := vm.services.Graphics.Pixel(vm.serviceOwner, image.surface, x-imageX, y-imageY)
			if pixelErr != nil {
				return false, pixelErr
			}
			if color.A != 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func (vm *VM) installTiledLayerNatives() {
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "<init>", "(IILjavax/microedition/lcdui/Image;II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		columns, _ := intArgument(args, 0)
		rows, _ := intArgument(args, 1)
		imageReference, _ := referenceArgument(args, 2)
		tileWidth, _ := intArgument(args, 3)
		tileHeight, _ := intArgument(args, 4)
		if columns <= 0 || rows <= 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		if err := vm.validateTileSet(imageReference, tileWidth, tileHeight); err != nil {
			return Value{}, false, err
		}
		_ = vm.initializeLayer(receiver, columns*tileWidth, rows*tileHeight)
		for field, value := range map[string]Value{gameColumnsField: IntValue(columns), gameRowsField: IntValue(rows), gameImageField: ReferenceValue(imageReference), gameFrameWidth: IntValue(tileWidth), gameFrameHeight: IntValue(tileHeight), gameCellsField: ReferenceValue(vm.newArray("[I", makeIntValues(int(columns*rows), 0))), gameAnimatedField: ReferenceValue(vm.newArray("[I", nil))} {
			_ = setObjectField(vm, receiver, field, value)
		}
		return Value{}, false, nil
	})
	for _, method := range []struct{ name, field string }{{"getColumns", gameColumnsField}, {"getRows", gameRowsField}, {"getCellWidth", gameFrameWidth}, {"getCellHeight", gameFrameHeight}} {
		vm.registerIntGetter("javax/microedition/lcdui/game/TiledLayer", method.name, method.field, 0)
	}
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "getCell", "(II)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, err := vm.tileCellIndex(receiver, args)
		if err != nil {
			return Value{}, false, err
		}
		cells, _ := vm.objectArray(receiver, gameCellsField, "[I")
		return cells.Elements[index], true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "setCell", "(III)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, err := vm.tileCellIndex(receiver, args)
		if err != nil {
			return Value{}, false, err
		}
		tile, _ := intArgument(args, 2)
		if err := vm.validateTileIndex(receiver, tile); err != nil {
			return Value{}, false, err
		}
		cells, _ := vm.objectArray(receiver, gameCellsField, "[I")
		cells.Elements[index] = IntValue(tile)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "fillCells", "(IIIII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		column, _ := intArgument(args, 0)
		row, _ := intArgument(args, 1)
		width, _ := intArgument(args, 2)
		height, _ := intArgument(args, 3)
		tile, _ := intArgument(args, 4)
		columns, _ := vm.gameInt(receiver, gameColumnsField)
		rows, _ := vm.gameInt(receiver, gameRowsField)
		if width < 0 || height < 0 || column < 0 || row < 0 || column+width > columns || row+height > rows {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		if err := vm.validateTileIndex(receiver, tile); err != nil {
			return Value{}, false, err
		}
		cells, _ := vm.objectArray(receiver, gameCellsField, "[I")
		for y := row; y < row+height; y++ {
			for x := column; x < column+width; x++ {
				cells.Elements[y*columns+x] = IntValue(tile)
			}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "createAnimatedTile", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		tile, _ := intArgument(args, 0)
		if tile < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		if err := vm.validateTileIndex(receiver, tile); err != nil {
			return Value{}, false, err
		}
		animated, _ := vm.objectArray(receiver, gameAnimatedField, "[I")
		animated.Elements = append(animated.Elements, IntValue(tile))
		return IntValue(-int32(len(animated.Elements))), true, nil
	})
	for _, name := range []string{"getAnimatedTile", "setAnimatedTile"} {
		name := name
		descriptor := "(I)I"
		if name == "setAnimatedTile" {
			descriptor = "(II)V"
		}
		vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", name, descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			index, _ := intArgument(args, 0)
			animated, _ := vm.objectArray(receiver, gameAnimatedField, "[I")
			if index >= 0 || -index > int32(len(animated.Elements)) {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			if name == "getAnimatedTile" {
				return animated.Elements[-index-1], true, nil
			}
			tile, _ := intArgument(args, 1)
			if tile < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
			}
			if err := vm.validateTileIndex(receiver, tile); err != nil {
				return Value{}, false, err
			}
			animated.Elements[-index-1] = IntValue(tile)
			return Value{}, false, nil
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "setStaticTileSet", "(Ljavax/microedition/lcdui/Image;II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		imageReference, _ := referenceArgument(args, 0)
		width, _ := intArgument(args, 1)
		height, _ := intArgument(args, 2)
		if err := vm.validateTileSet(imageReference, width, height); err != nil {
			return Value{}, false, err
		}
		_ = setObjectField(vm, receiver, gameImageField, ReferenceValue(imageReference))
		_ = setObjectField(vm, receiver, gameFrameWidth, IntValue(width))
		_ = setObjectField(vm, receiver, gameFrameHeight, IntValue(height))
		columns, _ := vm.gameInt(receiver, gameColumnsField)
		rows, _ := vm.gameInt(receiver, gameRowsField)
		_ = setObjectField(vm, receiver, gameWidthField, IntValue(columns*width))
		_ = setObjectField(vm, receiver, gameHeightField, IntValue(rows*height))
		cells, _ := vm.objectArray(receiver, gameCellsField, "[I")
		for index := range cells.Elements {
			cells.Elements[index] = IntValue(0)
		}
		animated, _ := vm.objectArray(receiver, gameAnimatedField, "[I")
		animated.Elements = nil
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/TiledLayer", "paint", "(Ljavax/microedition/lcdui/Graphics;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		return vm.paintTiledLayer(receiver, args)
	})
}

func makeIntValues(count int, value int32) []Value {
	values := make([]Value, count)
	for index := range values {
		values[index] = IntValue(value)
	}
	return values
}

func (vm *VM) validateTileSet(imageReference uint32, width, height int32) error {
	image, err := vm.image(imageReference)
	if err != nil {
		return vm.newThrowable("java/lang/NullPointerException", "")
	}
	if width <= 0 || height <= 0 || int32(image.width)%width != 0 || int32(image.height)%height != 0 {
		return vm.newThrowable("java/lang/IllegalArgumentException", "")
	}
	return nil
}

func (vm *VM) tileCellIndex(receiver uint32, args []Value) (int, error) {
	column, _ := intArgument(args, 0)
	row, _ := intArgument(args, 1)
	columns, _ := vm.gameInt(receiver, gameColumnsField)
	rows, _ := vm.gameInt(receiver, gameRowsField)
	if column < 0 || row < 0 || column >= columns || row >= rows {
		return 0, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	return int(row*columns + column), nil
}

func (vm *VM) validateTileIndex(receiver uint32, tile int32) error {
	if tile < 0 {
		animated, _ := vm.objectArray(receiver, gameAnimatedField, "[I")
		if -tile <= int32(len(animated.Elements)) {
			return nil
		}
		return vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	imageValue, _ := objectField(vm, receiver, gameImageField)
	imageReference, _ := imageValue.Reference()
	image, _ := vm.image(imageReference)
	width, _ := vm.gameInt(receiver, gameFrameWidth)
	height, _ := vm.gameInt(receiver, gameFrameHeight)
	count := int32(image.width) / width * (int32(image.height) / height)
	if tile > count {
		return vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
	}
	return nil
}

func (vm *VM) resolvedTile(receiver uint32, tile int32) int32 {
	if tile >= 0 {
		return tile
	}
	animated, _ := vm.objectArray(receiver, gameAnimatedField, "[I")
	value, _ := animated.Elements[-tile-1].Int()
	return value
}

func (vm *VM) paintTiledLayer(receiver uint32, args []Value) (Value, bool, error) {
	visible, _ := vm.gameInt(receiver, gameVisibleField)
	if visible == 0 {
		return Value{}, false, nil
	}
	graphicsReference, err := referenceArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	imageValue, _ := objectField(vm, receiver, gameImageField)
	imageReference, _ := imageValue.Reference()
	image, _ := vm.image(imageReference)
	columns, _ := vm.gameInt(receiver, gameColumnsField)
	rows, _ := vm.gameInt(receiver, gameRowsField)
	tileWidth, _ := vm.gameInt(receiver, gameFrameWidth)
	tileHeight, _ := vm.gameInt(receiver, gameFrameHeight)
	x, _ := vm.gameInt(receiver, gameXField)
	y, _ := vm.gameInt(receiver, gameYField)
	tileColumns := int32(image.width) / tileWidth
	cells, _ := vm.objectArray(receiver, gameCellsField, "[I")
	for row := int32(0); row < rows; row++ {
		for column := int32(0); column < columns; column++ {
			tile, _ := cells.Elements[row*columns+column].Int()
			tile = vm.resolvedTile(receiver, tile)
			if tile == 0 {
				continue
			}
			raw := tile - 1
			_, _, err := nativeDrawRegion(context.Background(), vm, graphicsReference, []Value{ReferenceValue(imageReference), IntValue((raw % tileColumns) * tileWidth), IntValue((raw / tileColumns) * tileHeight), IntValue(tileWidth), IntValue(tileHeight), IntValue(0), IntValue(x + column*tileWidth), IntValue(y + row*tileHeight), IntValue(0)})
			if err != nil {
				return Value{}, false, err
			}
		}
	}
	return Value{}, false, nil
}

func (vm *VM) spriteTiledCollision(sprite, tiled uint32, pixel bool) (bool, error) {
	if tiled == 0 {
		return false, vm.newThrowable("java/lang/NullPointerException", "")
	}
	spriteVisible, _ := vm.gameInt(sprite, gameVisibleField)
	tiledVisible, _ := vm.gameInt(tiled, gameVisibleField)
	if spriteVisible == 0 || tiledVisible == 0 {
		return false, nil
	}
	tiledX, _ := vm.gameInt(tiled, gameXField)
	tiledY, _ := vm.gameInt(tiled, gameYField)
	tiledWidth, _ := vm.gameInt(tiled, gameWidthField)
	tiledHeight, _ := vm.gameInt(tiled, gameHeightField)
	intersection, ok := intersectGameRects(vm.spriteCollisionRect(sprite), gameRect{tiledX, tiledY, tiledWidth, tiledHeight})
	if !ok {
		return false, nil
	}
	tileWidth, _ := vm.gameInt(tiled, gameFrameWidth)
	tileHeight, _ := vm.gameInt(tiled, gameFrameHeight)
	columns, _ := vm.gameInt(tiled, gameColumnsField)
	cells, _ := vm.objectArray(tiled, gameCellsField, "[I")
	imageValue, _ := objectField(vm, tiled, gameImageField)
	imageReference, _ := imageValue.Reference()
	image, _ := vm.image(imageReference)
	tileColumns := int32(image.width) / tileWidth
	for y := intersection.y; y < intersection.y+intersection.height; y++ {
		for x := intersection.x; x < intersection.x+intersection.width; x++ {
			column := (x - tiledX) / tileWidth
			row := (y - tiledY) / tileHeight
			tile, _ := cells.Elements[row*columns+column].Int()
			tile = vm.resolvedTile(tiled, tile)
			if tile == 0 {
				continue
			}
			if !pixel {
				return true, nil
			}
			opaque, err := vm.spriteWorldPixel(sprite, x, y)
			if err != nil || !opaque {
				if err != nil {
					return false, err
				}
				continue
			}
			raw := tile - 1
			color, err := vm.services.Graphics.Pixel(vm.serviceOwner, image.surface, (raw%tileColumns)*tileWidth+(x-tiledX)%tileWidth, (raw/tileColumns)*tileHeight+(y-tiledY)%tileHeight)
			if err != nil {
				return false, err
			}
			if color.A != 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func (vm *VM) installLayerManagerNatives() {
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "<init>", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		_ = setObjectField(vm, receiver, gameLayersField, ReferenceValue(vm.newArray("[Ljavax/microedition/lcdui/game/Layer;", nil)))
		_ = setObjectField(vm, receiver, gameViewWidthField, IntValue(int32(vm.ScreenWidth)))
		return Value{}, false, setObjectField(vm, receiver, gameViewHeightField, IntValue(int32(vm.canvasHeight())))
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "getSize", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		layers, _ := vm.objectArray(receiver, gameLayersField, "[Ljavax/microedition/lcdui/game/Layer;")
		return IntValue(int32(len(layers.Elements))), true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "getLayerAt", "(I)Ljavax/microedition/lcdui/game/Layer;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		index, _ := intArgument(args, 0)
		layers, _ := vm.objectArray(receiver, gameLayersField, "[Ljavax/microedition/lcdui/game/Layer;")
		if index < 0 || int(index) >= len(layers.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		return layers.Elements[index], true, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "append", "(Ljavax/microedition/lcdui/game/Layer;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		layers, _ := vm.objectArray(receiver, gameLayersField, "[Ljavax/microedition/lcdui/game/Layer;")
		return Value{}, false, vm.insertLayer(layers, args[0], int32(len(layers.Elements)))
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "insert", "(Ljavax/microedition/lcdui/game/Layer;I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		layers, _ := vm.objectArray(receiver, gameLayersField, "[Ljavax/microedition/lcdui/game/Layer;")
		index, _ := intArgument(args, 1)
		if index < 0 || int(index) > len(layers.Elements) {
			return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
		}
		return Value{}, false, vm.insertLayer(layers, args[0], index)
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "remove", "(Ljavax/microedition/lcdui/game/Layer;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		reference, _ := referenceArgument(args, 0)
		if reference == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		layers, _ := vm.objectArray(receiver, gameLayersField, "[Ljavax/microedition/lcdui/game/Layer;")
		for index, value := range layers.Elements {
			current, _ := value.Reference()
			if current == reference {
				layers.Elements = append(layers.Elements[:index], layers.Elements[index+1:]...)
				break
			}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "setViewWindow", "(IIII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		width, _ := intArgument(args, 2)
		height, _ := intArgument(args, 3)
		if width < 0 || height < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
		}
		for index, field := range []string{gameViewXField, gameViewYField, gameViewWidthField, gameViewHeightField} {
			_ = setObjectField(vm, receiver, field, args[index])
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/lcdui/game/LayerManager", "paint", "(Ljavax/microedition/lcdui/Graphics;II)V", func(ctx context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		graphics, _ := referenceArgument(args, 0)
		if graphics == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
		graphicsObject, err := vm.graphics(graphics)
		if err != nil {
			return Value{}, false, err
		}
		destinationX, _ := intArgument(args, 1)
		destinationY, _ := intArgument(args, 2)
		viewX, _ := vm.gameInt(receiver, gameViewXField)
		viewY, _ := vm.gameInt(receiver, gameViewYField)
		viewWidth, _ := vm.gameInt(receiver, gameViewWidthField)
		viewHeight, _ := vm.gameInt(receiver, gameViewHeightField)
		drawState, err := vm.services.Graphics.DrawState(vm.serviceOwner, graphicsObject.surface)
		if err != nil {
			return Value{}, false, err
		}
		clipped := drawState
		clipped.Clip = clipped.Clip.Intersect(graphicsClipRectangle(
			graphicsObject,
			drawState,
			destinationX,
			destinationY,
			viewWidth,
			viewHeight,
		))
		if err := vm.services.Graphics.SetDrawState(vm.serviceOwner, graphicsObject.surface, clipped); err != nil {
			return Value{}, false, err
		}
		restore := func() error {
			return vm.services.Graphics.SetDrawState(vm.serviceOwner, graphicsObject.surface, drawState)
		}
		layers, _ := vm.objectArray(receiver, gameLayersField, "[Ljavax/microedition/lcdui/game/Layer;")
		for index := len(layers.Elements) - 1; index >= 0; index-- {
			if err := vm.services.Graphics.SetDrawState(vm.serviceOwner, graphicsObject.surface, clipped); err != nil {
				_ = restore()
				return Value{}, false, err
			}
			layer, _ := layers.Elements[index].Reference()
			object, ok := vm.Object(layer)
			if !ok {
				continue
			}
			oldX, _ := vm.gameInt(layer, gameXField)
			oldY, _ := vm.gameInt(layer, gameYField)
			_ = setObjectField(vm, layer, gameXField, IntValue(oldX-viewX+destinationX))
			_ = setObjectField(vm, layer, gameYField, IntValue(oldY-viewY+destinationY))
			var paintErr error
			switch {
			case vm.classAssignable(object.Class, "javax/microedition/lcdui/game/Sprite"):
				_, _, err = vm.paintSprite(layer, []Value{ReferenceValue(graphics)})
				paintErr = err
			case vm.classAssignable(object.Class, "javax/microedition/lcdui/game/TiledLayer"):
				_, _, err = vm.paintTiledLayer(layer, []Value{ReferenceValue(graphics)})
				paintErr = err
			default:
				_, _, paintErr = vm.InvokeVirtual(ctx, layer, "paint", "(Ljavax/microedition/lcdui/Graphics;)V", ReferenceValue(graphics))
			}
			_ = setObjectField(vm, layer, gameXField, IntValue(oldX))
			_ = setObjectField(vm, layer, gameYField, IntValue(oldY))
			if paintErr != nil {
				_ = restore()
				return Value{}, false, paintErr
			}
		}
		return Value{}, false, restore()
	})
}

func (vm *VM) insertLayer(layers *Array, layer Value, index int32) error {
	reference, _ := layer.Reference()
	if reference == 0 {
		return vm.newThrowable("java/lang/NullPointerException", "")
	}
	for currentIndex, value := range layers.Elements {
		current, _ := value.Reference()
		if current == reference {
			layers.Elements = append(layers.Elements[:currentIndex], layers.Elements[currentIndex+1:]...)
			if int32(currentIndex) < index {
				index--
			}
			break
		}
	}
	layers.Elements = append(layers.Elements, Value{})
	copy(layers.Elements[index+1:], layers.Elements[index:len(layers.Elements)-1])
	layers.Elements[index] = layer
	return nil
}

func (vm *VM) installGameCanvasNatives() {
	vm.RegisterNative("javax/microedition/lcdui/game/GameCanvas", "<init>", "(Z)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.newImageState(vm.ScreenWidth, vm.canvasHeight())
		if err != nil {
			return Value{}, false, err
		}
		image := vm.NewObject("javax/microedition/lcdui/Image", state)
		graphics := vm.newGraphicsObject(&graphicsState{width: state.width, height: state.height, surface: state.surface, font: vm.defaultFont, color: 0xff000000})
		_ = setObjectField(vm, receiver, gameCanvasImage, ReferenceValue(image))
		_ = setObjectField(vm, receiver, gameCanvasGraphics, ReferenceValue(graphics))
		return Value{}, false, setObjectField(vm, receiver, "$game.suppressKeyEvents", args[0])
	})
	vm.RegisterNative("javax/microedition/lcdui/game/GameCanvas", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		value, err := objectField(vm, receiver, gameCanvasGraphics)
		return value, true, err
	})
	vm.RegisterNative("javax/microedition/lcdui/game/GameCanvas", "getKeyStates", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid GameCanvas")
		}
		states, _ := object.Fields["$game.keyStates"].Int()
		return IntValue(states), true, nil
	})
	for _, descriptor := range []string{"()V", "(IIII)V"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/lcdui/game/GameCanvas", "flushGraphics", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			imageValue, _ := objectField(vm, receiver, gameCanvasImage)
			imageReference, _ := imageValue.Reference()
			image, err := vm.image(imageReference)
			if err != nil {
				return Value{}, false, err
			}
			screen, err := vm.graphics(vm.ScreenGraphics())
			if err != nil {
				return Value{}, false, err
			}
			x, y, width, height := 0, 0, image.width, image.height
			if len(args) == 4 {
				xv, _ := intArgument(args, 0)
				yv, _ := intArgument(args, 1)
				wv, _ := intArgument(args, 2)
				hv, _ := intArgument(args, 3)
				x, y, width, height = int(xv), int(yv), int(wv), int(hv)
			}
			if width < 0 || height < 0 {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "")
			}
			return Value{}, false, blit(vm, screen, image, x, y, x, y, width, height)
		})
	}
	vm.RegisterNative("javax/microedition/lcdui/game/GameCanvas", "paint", "(Ljavax/microedition/lcdui/Graphics;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		destination, _ := referenceArgument(args, 0)
		graphics, err := vm.graphics(destination)
		if err != nil {
			return Value{}, false, err
		}
		imageValue, _ := objectField(vm, receiver, gameCanvasImage)
		imageReference, _ := imageValue.Reference()
		image, _ := vm.image(imageReference)
		return Value{}, false, blit(vm, graphics, image, 0, 0, 0, 0, image.width, image.height)
	})
}

func gameCanvasKeyMask(key int32) int32 {
	switch gameActionForKey(key) {
	case 1:
		return 0x0002
	case 2:
		return 0x0004
	case 5:
		return 0x0020
	case 6:
		return 0x0040
	case 8:
		return 0x0100
	case 9:
		return 0x0200
	case 10:
		return 0x0400
	case 11:
		return 0x0800
	case 12:
		return 0x1000
	default:
		return 0
	}
}
