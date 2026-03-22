import {ChangeDetectionStrategy, Component, inject, signal} from '@angular/core';
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
export class UploadDialogComponent {
  protected readonly files = signal<UploadFileEntry[]>([]);
  protected readonly collectionId = signal<number | null>(null);
  protected readonly isUploading = signal(false);
  protected readonly isDone = signal(false);

  private readonly dialogRef = inject(MatDialogRef<UploadDialogComponent>);
  private readonly libraryApi = inject(LibraryApiService);
  private readonly collectionsApi = inject(LibraryCollectionsApiService);

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

    this.isUploading.set(true);

    const fileNames = entries.map(e => e.file.name);
    const collectionId = this.collectionId();

    this.libraryApi.initiateUpload({fileNames, collectionId}).subscribe({
      next: (urlMap) => {
        this.uploadAll(entries, urlMap);
      },
      error: () => {
        this.isUploading.set(false);
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
        if (completed === total) this.onAllDone();
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
          if (completed === total) this.onAllDone();
        },
        error: () => {
          this.updateEntry(i, {status: 'error'});
          completed++;
          if (completed === total) this.onAllDone();
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

  private onAllDone(): void {
    this.isUploading.set(false);
    this.isDone.set(true);
  }

  protected close(): void {
    this.dialogRef.close(this.isDone());
  }

  protected formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
}
