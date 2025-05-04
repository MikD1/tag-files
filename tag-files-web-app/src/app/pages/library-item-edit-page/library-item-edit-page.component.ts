import {Component, effect, inject, signal} from '@angular/core';
import {ActivatedRoute} from '@angular/router';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {toSignal} from '@angular/core/rxjs-interop';
import {map} from 'rxjs';
import {MatChip, MatChipSet} from '@angular/material/chips';
import {MatIconModule} from '@angular/material/icon';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-library-item-edit-page',
  imports: [
    MatIconModule,
    MatChipSet,
    MatChip
  ],
  templateUrl: './library-item-edit-page.component.html',
  styleUrl: './library-item-edit-page.component.scss'
})
export class LibraryItemEditPageComponent {
  protected readonly item = signal<LibraryItem>(null!);
  private readonly route = inject(ActivatedRoute);
  private readonly libraryService = inject(LibraryApiService);
  private itemId = toSignal(this.route.paramMap.pipe(
    map(params => Number(params.get('id')))
  ));

  constructor() {
    effect(() => {
      const id = this.itemId();
      if (id == undefined || isNaN(id)) {
        return;
      }

      this.libraryService.getItem(id).subscribe((result) => {
        this.item.set(result);
      });
    });
  }

  protected getFullThumbnailPath(thumbnailPath?: string): string {
    if (!thumbnailPath) {
      return '#';
    }

    return ContentBaseUrl + thumbnailPath;
  }
}
