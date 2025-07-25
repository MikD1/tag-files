using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Contracts.LibraryCollections;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers.LibraryCollections;

public class GetLibraryCollectionHandler(AppDbContext dbContext)
    : IRequestHandler<GetLibraryCollectionRequest, LibraryCollectionWithItemsDto>
{
    public async Task<LibraryCollectionWithItemsDto> Handle(GetLibraryCollectionRequest request,
        CancellationToken cancellationToken)
    {
        LibraryCollection? collection =
            await dbContext.Collections.FirstOrDefaultAsync(x => x.Id == request.Id, cancellationToken);
        if (collection is null)
        {
            throw new ApplicationException($"LibraryCollection {request.Id} not found");
        }

        List<LibraryItem> items = await dbContext.LibraryItems
            .Where(x => x.CollectionId == collection.Id)
            .Include(x => x.Tags)
            .OrderByDescending(x => x.UploadedOn)
            .ToListAsync(cancellationToken);

        LibraryCollectionWithItemsDto dto = new(
            collection.Id,
            collection.Name,
            items.ConvertAll(LibraryItemDto.FromModel));
        return dto;
    }
}