import com.adobe.epubcheck.api.EPUBLocation;
import com.adobe.epubcheck.api.EpubCheck;
import com.adobe.epubcheck.api.MasterReport;
import com.adobe.epubcheck.messages.Message;
import com.adobe.epubcheck.messages.Severity;
import com.adobe.epubcheck.util.FeatureEnum;
import java.io.BufferedReader;
import java.io.File;
import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.io.InputStreamReader;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;

/**
 * Validates one EPUB per stdin line in a single JVM, so serial-sync pays EPUBCheck's
 * schema compilation once per run instead of once per file.
 *
 * Request: an EPUB path on one line. Response: one JSON object on one line,
 * {"path", "pass", "messages": [{"severity", "id", "path", "line", "column", "text"}], "exception"?}.
 * Pass/fail matches the epubcheck CLI: errors and fatals fail, warnings pass.
 */
public final class SerialSyncEPUBCheck {
  public static void main(String[] args) throws Exception {
    PrintStream out = new PrintStream(new FileOutputStream(FileDescriptor.out), true, "UTF-8");
    // EPUBCheck and its libraries print diagnostics to stdout; keep stdout for the protocol.
    System.setOut(System.err);
    BufferedReader in = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
    String path;
    while ((path = in.readLine()) != null) {
      JSONReport report = new JSONReport(path);
      boolean pass;
      String exception = null;
      try {
        int result = new EpubCheck(new File(path), report).doValidate();
        pass = (result & ~1) == 0;
      } catch (Exception e) {
        pass = false;
        exception = String.valueOf(e);
      }
      // A JVM Error propagates without a reply: serial-sync sees the exit and restarts us.
      StringBuilder response = new StringBuilder("{\"path\":");
      quote(response, path);
      response.append(",\"pass\":").append(pass).append(",\"messages\":[").append(report.messages).append(']');
      if (exception != null) {
        response.append(",\"exception\":");
        quote(response, exception);
      }
      out.println(response.append('}'));
      out.flush();
    }
  }

  static void quote(StringBuilder sb, String text) {
    sb.append('"');
    for (int i = 0; i < text.length(); i++) {
      char c = text.charAt(i);
      if (c == '"' || c == '\\') {
        sb.append('\\').append(c);
      } else if (c < 0x20) {
        sb.append(String.format("\\u%04x", (int) c));
      } else {
        sb.append(c);
      }
    }
    sb.append('"');
  }

  private static final class JSONReport extends MasterReport {
    final StringBuilder messages = new StringBuilder();

    JSONReport(String path) {
      setEpubFileName(path);
    }

    @Override
    protected void message(Message message, EPUBLocation location, Object... args) {
      if (message.getSeverity() == Severity.USAGE) {
        return;
      }
      String text = args != null && args.length > 0 ? message.getMessage(args) : message.getMessage();
      if (messages.length() > 0) {
        messages.append(',');
      }
      messages.append("{\"severity\":");
      quote(messages, String.valueOf(message.getSeverity()));
      messages.append(",\"id\":");
      quote(messages, String.valueOf(message.getID()));
      messages.append(",\"path\":");
      quote(messages, String.valueOf(location.getPath()));
      messages.append(",\"line\":").append(location.getLine());
      messages.append(",\"column\":").append(location.getColumn());
      messages.append(",\"text\":");
      quote(messages, String.valueOf(text));
      messages.append('}');
    }

    @Override
    public void info(String resource, FeatureEnum feature, String value) {}

    @Override
    public int generate() {
      return 0;
    }

    @Override
    public void initialize() {}
  }
}
