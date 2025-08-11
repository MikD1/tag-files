import {ChangeDetectionStrategy, Component, effect, inject, signal} from '@angular/core';
import {LibraryCollection, LibraryCollectionsApiService} from '../../services/api/library-collections-api.service';
import {MatTableModule} from '@angular/material/table';
import {MatButtonModule} from '@angular/material/button';
import {MatChipsModule} from '@angular/material/chips';
import {MatListModule} from '@angular/material/list';
import {MatIconModule} from '@angular/material/icon';
import {MatDialog} from '@angular/material/dialog';
import {RouterLink} from '@angular/router';
import {CollectionEditModalComponent} from '../collection-edit-modal/collection-edit-modal.component';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-collections-table',
  standalone: true,
  imports: [
    MatTableModule,
    MatButtonModule,
    MatChipsModule,
    MatListModule,
    MatIconModule,
    RouterLink
  ],
  templateUrl: './collections-page.component.html',
  styleUrl: './collections-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CollectionsPageComponent {
  protected readonly collections = signal<LibraryCollection[]>([]);
  protected readonly displayedColumns = ['cover', 'name', 'actions'];
  private readonly collectionsService = inject(LibraryCollectionsApiService);
  private dialog = inject(MatDialog);

  constructor() {
    effect(() => {
      this.loadCollections();
    });
  }

  protected addCollection() {
    const dialogRef = this.dialog.open(CollectionEditModalComponent, {
      data: {
        collection: null,
      },
      width: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        this.loadCollections();
      }
    });
  }

  protected getFullCoverPath(coverPath?: string | null): string {
    if (!coverPath) {
      return 'https://via.placeholder.com/80x80?text=No+Cover';
    }

    return ContentBaseUrl + coverPath;
  }

  private loadCollections() {
    this.collectionsService.getCollections().subscribe((result) => {
      this.collections.set(result);
    });
  }
}
