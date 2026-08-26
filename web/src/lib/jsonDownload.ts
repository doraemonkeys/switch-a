export interface FileDownloadPort {
  save(file: Blob, filename: string): void;
}

export const browserFileDownloadPort: FileDownloadPort = {
  save(file, filename) {
    const objectUrl = URL.createObjectURL(file);
    const link = document.createElement("a");
    link.href = objectUrl;
    link.download = filename;
    try {
      document.body.appendChild(link);
      link.click();
    } finally {
      // Object URLs retain the credential bytes until explicitly released.
      link.remove();
      URL.revokeObjectURL(objectUrl);
    }
  },
};

export function downloadJsonFile(
  filename: string,
  value: unknown,
  port: FileDownloadPort = browserFileDownloadPort,
): void {
  port.save(
    new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
    filename,
  );
}
