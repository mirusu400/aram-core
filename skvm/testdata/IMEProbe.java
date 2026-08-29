// Test double for the XCE TextComponent callback interface. The XCE input
// method pushes composed glyphs in through insert/replace/delete; this records
// them into a static buffer the Go test reads back through length()/at().
// Compiled to skvm/testdata/IMEProbe.class (see the sibling .java).
public class IMEProbe {
    static char[] buf = new char[64];
    static int len = 0;
    public void insert(char c) { buf[len] = c; len = len + 1; }
    public void replace(char c) { if (len > 0) { buf[len - 1] = c; } else { buf[len] = c; len = len + 1; } }
    public void delete() { if (len > 0) { len = len - 1; } }
    public static int length() { return len; }
    public static int at(int i) { return buf[i]; }
}
