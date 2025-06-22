using MediatR;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Logging;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class GetLibraryItemByIdHandler(ILogger<GetLibraryItemByIdHandler> logger, AppDbContext dbContext)
    : IRequestHandler<GetLibraryItemByIdRequest, LibraryItemDto?>
{
    public async Task<LibraryItemDto?> Handle(GetLibraryItemByIdRequest request, CancellationToken cancellationToken)
    {
        LibraryItem? libraryItem = await dbContext.LibraryItems
            .Where(x => x.Id == request.Id)
            .Include(x => x.Tags)
            .SingleOrDefaultAsync(cancellationToken);
        if (libraryItem is null)
        {
            logger.LogWarning("Library item with ID {id} not found.", request.Id);
            return null;
        }

        return LibraryItemDto.FromModel(libraryItem);
    }
}