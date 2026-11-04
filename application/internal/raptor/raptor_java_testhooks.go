package raptor

// InstallJavaCardForTest gives r a current Java card whose class declares
// keyNotify(II)Z and paint(Graphics)V at the given guest bodies, with a full
// repaint already requested. Machine-level scheduling tests use it to drive the
// frame loop without a title.
func (r *Runtime) InstallJavaCardForTest(keyNotify, paint uint32) (uint32, error) {
	if r.resolvedImports == nil {
		r.resolvedImports = make(map[raptorImportKey]uint64)
	}
	if r.importSlotByKey == nil {
		r.importSlotByKey = make(map[raptorImportKey]uint32)
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return 0, err
	}
	holder, err := r.Public.Heap.Allocate(80, true)
	if err != nil {
		return 0, err
	}
	class := &raptorJavaClass{
		Name:       "test/Card",
		Holder:     holder,
		parentName: "org/kwis/msp/lcdui/Card",
		methods: []raptorJavaDeclaredMethod{
			{Name: "keyNotify", descriptor: "(II)Z", Body: keyNotify},
			{Name: "paint", descriptor: "(Lorg/kwis/msp/lcdui/Graphics;)V", Body: paint},
		},
	}
	java.classes[holder], java.ClassByName[class.Name] = class, class
	card, err := r.NewRaptorJavaObject(holder)
	if err != nil {
		return 0, err
	}
	java.currentCard = card
	r.queueRaptorJavaCardRepaint(java, card, nil)
	return card, nil
}
