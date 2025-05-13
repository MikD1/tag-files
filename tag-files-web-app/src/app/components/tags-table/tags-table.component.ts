import {ChangeDetectionStrategy, Component, effect, inject, signal} from '@angular/core';
import {TagsApiService, TagStatistics} from '../../services/api/tags-api.service';
import {MatTableModule} from '@angular/material/table';
import {MatButtonModule} from '@angular/material/button';
import {MatChipsModule} from '@angular/material/chips';
import {MatListModule} from '@angular/material/list';
import {MatIconModule} from '@angular/material/icon';
import {SearchService} from '../../services/search.service';
import {MatDialog} from '@angular/material/dialog';
import {TagEditModalComponent} from '../../pages/tag-edit-modal/tag-edit-modal.component';
import {Router} from '@angular/router';

@Component({
  selector: 'app-tags-table',
  imports: [
    MatTableModule,
    MatButtonModule,
    MatChipsModule,
    MatListModule,
    MatIconModule
  ],
  templateUrl: './tags-table.component.html',
  styleUrl: './tags-table.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class TagsTableComponent {
  protected readonly tags = signal<TagStatistics[]>([]);
  protected readonly displayedColumns = ['tag', 'usageCount', 'actions'];
  private readonly tagsService = inject(TagsApiService);
  private readonly searchService = inject(SearchService);
  private dialog = inject(MatDialog);
  private readonly router = inject(Router);

  constructor() {
    effect(() => {
      this.tagsService.getTagStatistics().subscribe((result) => {
        this.tags.set(result);
      });
    });
  }

  protected tagClick(tag: string) {
    this.searchService.searchQuery.setValue(tag);
    this.searchService.isVideoSelected.update(() => false)
    this.searchService.isImageSelected.update(() => false)
    this.searchService.search();
    this.router.navigate(['/library']);
  }

  protected editItem(tag: string) {
    const dialogRef = this.dialog.open(TagEditModalComponent, {
      data: {
        tag: tag
      }
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        window.location.reload();
      }
    });
  }
}
