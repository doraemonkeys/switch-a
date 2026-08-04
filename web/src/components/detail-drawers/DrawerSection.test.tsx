import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DetailSection, DetailRow } from "./DrawerSection";

describe("DetailSection", () => {
  it("renders title correctly", () => {
    render(
      <DetailSection title="Test Section">
        <div>Content</div>
      </DetailSection>,
    );
    expect(screen.getByText("Test Section")).toBeInTheDocument();
  });

  it("renders children content", () => {
    render(
      <DetailSection title="Section">
        <span>Child Content</span>
      </DetailSection>,
    );
    expect(screen.getByText("Child Content")).toBeInTheDocument();
  });

  it("renders action when provided", () => {
    render(
      <DetailSection title="Section" action={<button>Edit</button>}>
        <div>Content</div>
      </DetailSection>,
    );
    expect(screen.getByText("Edit")).toBeInTheDocument();
  });

  it("does not render action when not provided", () => {
    render(
      <DetailSection title="Section">
        <div>Content</div>
      </DetailSection>,
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("DetailRow", () => {
  it("renders label and value", () => {
    render(<DetailRow label="Name" value="John Doe" />);
    expect(screen.getByText("Name")).toBeInTheDocument();
    expect(screen.getByText("John Doe")).toBeInTheDocument();
  });

  it("applies mono class when mono prop is true", () => {
    render(<DetailRow label="ID" value="abc123" mono />);
    const valueElement = screen.getByText("abc123");
    expect(valueElement).toHaveClass("font-mono");
    expect(valueElement).toHaveClass("text-xs");
  });

  it("applies font-medium class when mono prop is false", () => {
    render(<DetailRow label="Status" value="Active" mono={false} />);
    const valueElement = screen.getByText("Active");
    expect(valueElement).toHaveClass("font-medium");
  });

  it("applies font-medium class when mono prop is not provided", () => {
    render(<DetailRow label="Status" value="Active" />);
    const valueElement = screen.getByText("Active");
    expect(valueElement).toHaveClass("font-medium");
  });

  it("renders ReactNode as value", () => {
    render(<DetailRow label="Link" value={<a href="/test">Click here</a>} />);
    expect(screen.getByText("Click here")).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", "/test");
  });
});
