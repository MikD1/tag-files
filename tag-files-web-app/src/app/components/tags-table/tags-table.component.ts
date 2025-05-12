import {ChangeDetectionStrategy, Component, effect, inject, signal} from '@angular/core';
import {TagsApiService, TagStatistics} from '../../services/api/tags-api.service';
import {MatTableModule} from '@angular/material/table';
import {MatButtonModule} from '@angular/material/button';
import {MatChipsModule} from '@angular/material/chips';
import {MatListModule} from '@angular/material/list';
import {MatIconModule} from '@angular/material/icon';

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

  constructor() {
    effect(() => {
      this.tagsService.getTagStatistics().subscribe((result) => {
        this.tags.set(result);
      });
    });
  }
}
