import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ConfirmModal } from "./ConfirmModal";

describe("ConfirmModal", () => {
  const defaultProps = {
    isOpen: true,
    onClose: vi.fn(),
    onConfirm: vi.fn(),
    title: "Confirm Action",
    message: "Are you sure you want to proceed?",
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("Visibility", () => {
    it("renders when isOpen is true", () => {
      render(<ConfirmModal {...defaultProps} />);
      expect(screen.getByText("Confirm Action")).toBeInTheDocument();
      expect(
        screen.getByText("Are you sure you want to proceed?"),
      ).toBeInTheDocument();
    });

    it("does not render when isOpen is false", () => {
      render(<ConfirmModal {...defaultProps} isOpen={false} />);
      expect(screen.queryByText("Confirm Action")).not.toBeInTheDocument();
    });
  });

  describe("Default props", () => {
    it("shows default confirm and cancel text", () => {
      render(<ConfirmModal {...defaultProps} />);
      expect(screen.getByText("Confirm")).toBeInTheDocument();
      expect(screen.getByText("Cancel")).toBeInTheDocument();
    });

    it("uses custom confirm and cancel text", () => {
      render(
        <ConfirmModal
          {...defaultProps}
          confirmText="Delete"
          cancelText="Go Back"
        />,
      );
      expect(screen.getByText("Delete")).toBeInTheDocument();
      expect(screen.getByText("Go Back")).toBeInTheDocument();
    });
  });

  describe("Button interactions", () => {
    it("calls onConfirm when confirm button is clicked", () => {
      render(<ConfirmModal {...defaultProps} />);
      fireEvent.click(screen.getByText("Confirm"));
      expect(defaultProps.onConfirm).toHaveBeenCalled();
    });

    it("calls onClose when cancel button is clicked", () => {
      render(<ConfirmModal {...defaultProps} />);
      fireEvent.click(screen.getByText("Cancel"));
      expect(defaultProps.onClose).toHaveBeenCalled();
    });

    it("calls onClose when close button is clicked", () => {
      render(<ConfirmModal {...defaultProps} />);
      fireEvent.click(screen.getByLabelText("Close"));
      expect(defaultProps.onClose).toHaveBeenCalled();
    });
  });

  describe("Escape key handling", () => {
    it("closes modal on Escape key press", () => {
      render(<ConfirmModal {...defaultProps} />);
      fireEvent.keyDown(document, { key: "Escape" });
      expect(defaultProps.onClose).toHaveBeenCalled();
    });

    it("does not close modal on Escape when loading", () => {
      render(<ConfirmModal {...defaultProps} loading={true} />);
      fireEvent.keyDown(document, { key: "Escape" });
      expect(defaultProps.onClose).not.toHaveBeenCalled();
    });
  });

  describe("Loading state", () => {
    it("shows 'Processing...' text when loading", () => {
      render(<ConfirmModal {...defaultProps} loading={true} />);
      expect(screen.getByText("Processing...")).toBeInTheDocument();
    });

    it("disables buttons when loading", () => {
      render(<ConfirmModal {...defaultProps} loading={true} />);
      expect(screen.getByText("Cancel")).toBeDisabled();
      expect(screen.getByText("Processing...")).toBeDisabled();
      expect(screen.getByLabelText("Close")).toBeDisabled();
    });
  });

  describe("Variant styles", () => {
    it("applies danger variant style", () => {
      render(<ConfirmModal {...defaultProps} variant="danger" />);
      const confirmButton = screen.getByText("Confirm");
      expect(confirmButton).toHaveClass("bg-red-500");
    });

    it("applies warning variant style", () => {
      render(<ConfirmModal {...defaultProps} variant="warning" />);
      const confirmButton = screen.getByText("Confirm");
      expect(confirmButton).toHaveClass("bg-yellow-500");
    });

    it("applies default variant style", () => {
      render(<ConfirmModal {...defaultProps} variant="default" />);
      const confirmButton = screen.getByText("Confirm");
      expect(confirmButton).toHaveClass("btn-primary");
    });

    it("uses default variant when not specified", () => {
      render(<ConfirmModal {...defaultProps} />);
      const confirmButton = screen.getByText("Confirm");
      expect(confirmButton).toHaveClass("btn-primary");
    });
  });

  describe("Keyboard cleanup", () => {
    it("removes keydown listener when modal closes", () => {
      const removeEventListenerSpy = vi.spyOn(document, "removeEventListener");

      const { rerender } = render(<ConfirmModal {...defaultProps} />);
      rerender(<ConfirmModal {...defaultProps} isOpen={false} />);

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "keydown",
        expect.any(Function),
      );
      removeEventListenerSpy.mockRestore();
    });
  });
});
