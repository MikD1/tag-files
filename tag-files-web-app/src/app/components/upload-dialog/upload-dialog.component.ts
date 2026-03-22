import {ChangeDetectionStrategy, Component, inject, OnDestroy, signal} from '@angular/core';
import {
  MatDialogActions,
  MatDialogContent,
  MatDialogRef,
  MatDialogTitle,
} from '@angular/material/dialog';
import {MatButton} from '@angular/material/button';
import {MatProgressBarModule} from '@angular/material/progress-bar';
import {MatIconModule} from '@angular/material/icon';
import {MatListModule} from '@angular/material/list';
import {MatInputModule} from '@angular/material/input';
import {MatFormFieldModule} from '@angular/material/form-field';
import {FormsModule} from '@angular/forms';
import {LibraryApiService} from '../../services/api/library-api.service';
import {LibraryCollectionsApiService} from '../../services/api/library-collections-api.service';
import {FilesProcessingApiService, ProcessingFileDto, ProcessingStatus} from '../../services/api/files-processing-api.service';
import {SelectCollectionComponent} from '../select-collection/select-collection.component';

export interface UploadFileEntry {
  file: File;
  progress: number;
  status: 'pending' | 'uploading' | 'done' | 'error';
}

@Component({
  selector: 'app-upload-dialog',
  standalone: true,
  imports: [
    MatDialogTitle,
    MatDialogContent,
    MatDialogActions,
    MatButton,
    MatProgressBarModule,
    MatIconModule,
    MatListModule,
    MatInputModule,
    MatFormFieldModule,
    FormsModule,
    SelectCollectionComponent,
  ],
  templateUrl: './upload-dialog.component.html',
  styleUrl: './upload-dialog.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class UploadDialogComponent implements OnDestroy {
  protected readonly files = signal<UploadFileEntry[]>([]);
  protected readonly collectionId = signal<number | null>(null);
  protected readonly phase = signal<'selecting' | 'uploading' | 'processing' | 'done'>('selecting');
  protected readonly processingFiles = signal<ProcessingFileDto[]>([]);

  private readonly dialogRef = inject(MatDialogRef<UploadDialogComponent>);
  private readonly libraryApi = inject(LibraryApiService);
  private readonly collectionsApi = inject(LibraryCollectionsApiService);
  private readonly processingApi = inject(FilesProcessingApiService);
  private pollingInterval?: ReturnType<typeof setInterval>;

  protected get isUploading(): boolean {
    return this.phase() === 'uploading';
  }

  protected get isDone(): boolean {
    return this.phase() === 'done';
  }

  protected onFilesSelected(event: Event): void {
    const input = event.target as HTMLInputElement;
    if (!input.files) return;
    const entries: UploadFileEntry[] = Array.from(input.files).map(file => ({
      file,
      progress: 0,
      status: 'pending',
    }));
    this.files.set(entries);
  }

  protected removeFile(index: number): void {
    this.files.update(f => f.filter((_, i) => i !== index));
  }

  protected async upload(): Promise<void> {
    const entries = this.files();
    if (entries.length === 0) return;

    this.phase.set('uploading');

    const fileNames = entries.map(e => e.file.name);
    const collectionId = this.collectionId();

    this.libraryApi.initiateUpload({fileNames, collectionId}).subscribe({
      next: (urlMap) => {
        this.uploadAll(entries, urlMap);
      },
      error: () => {
        this.phase.set('selecting');
      }
    });
  }

  private uploadAll(entries: UploadFileEntry[], urlMap: Record<string, string>): void {
    let completed = 0;
    const total = entries.length;

    for (let i = 0; i < entries.length; i++) {
      const entry = entries[i];
      const url = urlMap[entry.file.name];
      if (!url) {
        this.updateEntry(i, {status: 'error', progress: 0});
        completed++;
        if (completed === total) this.startProcessingPhase();
        continue;
      }

      this.updateEntry(i, {status: 'uploading', progress: 0});

      this.libraryApi.uploadFile(url, entry.file).subscribe({
        next: (progress) => {
          this.updateEntry(i, {progress});
        },
        complete: () => {
          this.updateEntry(i, {status: 'done', progress: 100});
          completed++;
          if (completed === total) this.startProcessingPhase();
        },
        error: () => {
          this.updateEntry(i, {status: 'error'});
          completed++;
          if (completed === total) this.startProcessingPhase();
        },
      });
    }
  }

  private updateEntry(index: number, patch: Partial<UploadFileEntry>): void {
    this.files.update(entries => {
      const updated = [...entries];
      updated[index] = {...updated[index], ...patch};
      return updated;
    });
  }

  private startProcessingPhase(): void {
    this.phase.set('processing');
    this.pollProcessingStatus();
    this.pollingInterval = setInterval(() => this.pollProcessingStatus(), 2500);
  }

  private pollProcessingStatus(): void {
    this.processingApi.getProcessingFiles().subscribe({
      next: (files) => {
        this.processingFiles.set(files);
        if (files.length === 0) {
          this.stopPolling();
          this.phase.set('done');
        }
      },
    });
  }

  private stopPolling(): void {
    if (this.pollingInterval !== undefined) {
      clearInterval(this.pollingInterval);
      this.pollingInterval = undefined;
    }
  }

  ngOnDestroy(): void {
    this.stopPolling();
  }

  protected close(): void {
    this.stopPolling();
    this.dialogRef.close(this.isDone);
  }

  protected statusLabel(status: ProcessingStatus): string {
    switch (status) {
      case 'WaitingForUpload':
      case 'Pending':
        return 'Waiting...';
      case 'Converting':
        return 'Converting...';
      case 'AddedToLibrary':
      case 'TemporaryFileDeleted':
      case 'LibraryItemSaved':
        return 'Finalizing...';
      default:
        return status;
    }
  }

  protected formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
}
