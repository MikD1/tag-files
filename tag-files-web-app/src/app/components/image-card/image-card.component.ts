import { ChangeDetectionStrategy, Component, computed, input, Input, } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { LibraryItem } from '../../services/library.service';

@Component({
  selector: 'app-image-card',
  imports: [MatCardModule, MatButtonModule],
  templateUrl: './image-card.component.html',
  styleUrl: './image-card.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageCardComponent {
  libraryItem = input.required<LibraryItem>();

  protected thumbnailPath = computed(() => {
    let thumbnailPath = this.libraryItem().thumbnailPath;
    if (thumbnailPath) {
      return 'http://localhost:5010/' + thumbnailPath;
    } else {
      return '#' // TODO: return placeholder img path
    }
  })
}
